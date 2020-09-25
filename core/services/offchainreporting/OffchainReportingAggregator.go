package offchainreporting

import (
	"context"
	"math/big"
	"strings"
	"sync"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	gethCommon "github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/chainlink/core/services/eth"
	"github.com/smartcontractkit/chainlink/core/store/models"
	"github.com/smartcontractkit/chainlink/offchainreporting/confighelper"
	"github.com/smartcontractkit/chainlink/offchainreporting/gethwrappers/offchainaggregator"
	ocrtypes "github.com/smartcontractkit/chainlink/offchainreporting/types"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/pkg/errors"
)

const (
	OffchainReportingAggregatorName = "OffchainReportingAggregator"
)

var (
	OffchainReportingAggregatorConfigSet = getConfigSetHash()

	OffchainReportingAggregatorLogTypes = map[gethCommon.Hash]eth.Log{
		OffchainReportingAggregatorConfigSet: &LogConfigSet{},
	}
)

type (
	LogConfigSet struct {
		eth.GethRawLog
		ConfigCount  uint64
		Oracles      []gethCommon.Address
		Transmitters []gethCommon.Address
		Threshold    uint8
		Config       []byte
	}

	OffchainReportingAggregator struct {
		ethClient        eth.Client
		configSetChan    chan offchainaggregator.OffchainAggregatorConfigSet
		contractFilterer *offchainaggregator.OffchainAggregatorFilterer
		contractCaller   *offchainaggregator.OffchainAggregatorCaller
		contractAddress  gethCommon.Address
		logBroadcaster   eth.LogBroadcaster

		jobID       models.ID
		transmitter Transmitter
		contractABI abi.ABI
	}

	Transmitter interface {
		CreateEthTransaction(ctx context.Context, toAddress gethCommon.Address, payload []byte) error
		FromAddress() gethCommon.Address
	}
)

var (
	_ ocrtypes.ContractConfigTracker      = &OffchainReportingAggregator{}
	_ ocrtypes.ContractTransmitter        = &OffchainReportingAggregator{}
	_ ocrtypes.ContractConfigSubscription = &OffchainReportingAggregatorConfigSubscription{}
)

func NewOffchainReportingAggregator(address gethCommon.Address, ethClient eth.Client, logBroadcaster eth.LogBroadcaster, jobID models.ID, transmitter Transmitter) (o *OffchainReportingAggregator, err error) {
	contractFilterer, err := offchainaggregator.NewOffchainAggregatorFilterer(address, ethClient)
	if err != nil {
		return o, err
	}

	contractCaller, err := offchainaggregator.NewOffchainAggregatorCaller(address, ethClient)
	if err != nil {
		return o, err
	}

	contractABI, err := abi.JSON(strings.NewReader(offchainaggregator.OffchainAggregatorABI))
	if err != nil {
		return o, err
	}

	return &OffchainReportingAggregator{
		contractFilterer: contractFilterer,
		contractCaller:   contractCaller,
		contractAddress:  address,
		logBroadcaster:   logBroadcaster,
		ethClient:        ethClient,
		configSetChan:    make(chan offchainaggregator.OffchainAggregatorConfigSet),
		jobID:            jobID,
		transmitter:      transmitter,
		contractABI:      contractABI,
	}, nil
}

func (ra *OffchainReportingAggregator) Transmit(ctx context.Context, report []byte, rs, ss [][32]byte, vs [32]byte) error {
	payload, err := ra.contractABI.Pack("transmit", report, rs, ss, vs)
	if err != nil {
		return errors.Wrap(err, "abi.Pack failed")
	}

	return errors.Wrap(ra.transmitter.CreateEthTransaction(ctx, ra.contractAddress, payload), "failed to send Eth transaction")
}

func (ra *OffchainReportingAggregator) SubscribeToNewConfigs(ctx context.Context) (ocrtypes.ContractConfigSubscription, error) {
	sub := &OffchainReportingAggregatorConfigSubscription{
		make(chan ocrtypes.ContractConfig),
		ra,
		sync.Mutex{},
		false,
	}
	connected := ra.logBroadcaster.Register(ra.contractAddress, sub)
	if !connected {
		return nil, errors.New("Failed to register with logBroadcaster")
	}

	return sub, nil
}

func (ra *OffchainReportingAggregator) LatestConfigDetails(ctx context.Context) (changedInBlock uint64, configDigest ocrtypes.ConfigDigest, err error) {
	result, err := ra.contractCaller.LatestConfigDetails(nil)
	if err != nil {
		return 0, configDigest, errors.Wrap(err, "error getting LatestConfigDetails")
	}
	return uint64(result.BlockNumber), ocrtypes.BytesToConfigDigest(result.ConfigDigest[:]), err
}

// Conform OffchainReportingAggregator to LogListener interface
type OffchainReportingAggregatorConfigSubscription struct {
	ch       chan ocrtypes.ContractConfig
	ra       *OffchainReportingAggregator
	mutex    sync.Mutex
	chClosed bool
}

func (sub *OffchainReportingAggregatorConfigSubscription) OnConnect() {}
func (sub *OffchainReportingAggregatorConfigSubscription) OnDisconnect() {
	sub.mutex.Lock()
	defer sub.mutex.Unlock()

	if !sub.chClosed {
		sub.chClosed = true
		close(sub.ch)
	}
}
func (sub *OffchainReportingAggregatorConfigSubscription) HandleLog(lb eth.LogBroadcast, err error) {
	topics := lb.Log().RawLog().Topics
	if len(topics) == 0 {
		return
	}
	switch topics[0] {
	case OffchainReportingAggregatorConfigSet:
		configSet, err := sub.ra.contractFilterer.ParseConfigSet(lb.Log().RawLog())
		if err != nil {
			panic(err)
		}
		configSet.Raw = lb.Log().RawLog()
		cc := confighelper.ContractConfigFromConfigSetEvent(*configSet)
		sub.ch <- cc
	default:
	}
}
func (sub *OffchainReportingAggregatorConfigSubscription) JobID() *models.ID {
	jobID := sub.ra.jobID
	return &jobID
}
func (sub *OffchainReportingAggregatorConfigSubscription) Configs() <-chan ocrtypes.ContractConfig {
	return sub.ch
}
func (sub *OffchainReportingAggregatorConfigSubscription) Close() {
	sub.ra.logBroadcaster.Unregister(sub.ra.contractAddress, sub)
}

func (ra *OffchainReportingAggregator) ConfigFromLogs(ctx context.Context, changedInBlock uint64) (c ocrtypes.ContractConfig, err error) {
	q := ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(changedInBlock)),
		ToBlock:   big.NewInt(int64(changedInBlock)),
		Addresses: []gethCommon.Address{ra.contractAddress},
		Topics: [][]gethCommon.Hash{
			{OffchainReportingAggregatorConfigSet},
		},
	}

	logs, err := ra.ethClient.FilterLogs(ctx, q)
	if err != nil {
		return c, err
	}
	if len(logs) == 0 {
		return c, errors.New("no logs")
	}

	latest, err := ra.contractFilterer.ParseConfigSet(logs[len(logs)-1])
	if err != nil {
		panic(err)
	}
	latest.Raw = logs[len(logs)-1]
	return confighelper.ContractConfigFromConfigSetEvent(*latest), err
}

func (ra *OffchainReportingAggregator) LatestBlockHeight(ctx context.Context) (blockheight uint64, err error) {
	h, err := ra.ethClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, err
	}
	if h == nil {
		return 0, errors.New("got nil head")
	}

	return uint64(h.Number), nil
}

func (ra *OffchainReportingAggregator) LatestTransmissionDetails(ctx context.Context) (configDigest ocrtypes.ConfigDigest, epoch uint32, round uint8, latestAnswer ocrtypes.Observation, latestTimestamp time.Time, err error) {
	result, err := ra.contractCaller.LatestTransmissionDetails(nil)
	if err != nil {
		return configDigest, 0, 0, ocrtypes.Observation(nil), time.Time{}, errors.Wrap(err, "error getting LatestTransmissionDetails")
	}
	return result.ConfigDigest, result.Epoch, result.Round, ocrtypes.Observation(result.LatestAnswer), time.Unix(int64(result.LatestTimestamp), 0), nil
}

func getConfigSetHash() gethCommon.Hash {
	abi, err := abi.JSON(strings.NewReader(offchainaggregator.OffchainAggregatorABI))
	if err != nil {
		panic("could not parse OffchainAggregator ABI: " + err.Error())
	}
	return abi.Events["ConfigSet"].ID
}

func (ra *OffchainReportingAggregator) FromAddress() gethCommon.Address {
	return ra.transmitter.FromAddress()
}
