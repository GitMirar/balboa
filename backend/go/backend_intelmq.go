package backend

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io/ioutil"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/DCSO/balboa/db"
	"github.com/DCSO/balboa/observation"
	log "github.com/sirupsen/logrus"
)

const (
	intelMqDialTimeout            = 60 * time.Second
	intelMqObservationsBufferSize = 0x1000 * 0x100
	intelMqConnectionAttempts     = 10
	intelMqTcpFlushTimeout        = 1 * time.Second
)

type IntelMqInput struct {
	ClassificationTaxonomy string `json:"classification.taxonomy"`
	ClassificationType     string `json:"classification.type"`
	FeedName               string `json:"feed.name"`
	FeedProvider           string `json:"feed.provider"`
	TimeObservation        string `json:"time.observation"`
	TimeSource             string `json:"time.source"`
	SourceFqdn             string `json:"source.fqdn"`
	Raw                    string `json:"raw"`
}

type IntelMqHandler struct {
	intelMqCollector    string // hostname:port of IntelMQ TCP collector
	intelMqFeedName     string // feed name in IntelMQ after JSON parser
	intelMqFeedProvider string // feed provider in IntelMQ after JSON parser
	selectorFile        string // file containing newline separated regular expressions
	stopChan            chan bool
	stoppedChan         chan bool
	observations        chan *observation.InputObservation
	conn                net.Conn
	connWriter          *bufio.Writer
	selectors           []*regexp.Regexp
}

func NewIntelMqHandler(intelMqCollector string, intelMqFeedName string, intelMqFeedProvider string, selectorFile string) *IntelMqHandler {
	i := &IntelMqHandler{intelMqCollector: intelMqCollector,
		intelMqFeedName:     intelMqFeedName,
		intelMqFeedProvider: intelMqFeedProvider,
		selectorFile:        selectorFile,
	}
	i.stopChan = make(chan bool)
	i.stoppedChan = make(chan bool)
	i.observations = make(chan *observation.InputObservation, intelMqObservationsBufferSize)
	if i.selectorFile != "" {
		i.loadSelectors()
	}
	go i.tcpWorker()
	return i
}

func (i *IntelMqHandler) loadSelectors() {
	selectorsRaw, err := ioutil.ReadFile(i.selectorFile)
	if err != nil {
		log.Fatalf("could not read selector file due to %v", err)
	}
	for _, s := range strings.Split(string(selectorsRaw), "\n") {
		if s == "" {
			continue
		}
		r := regexp.MustCompile(s)
		if r == nil {
			log.Fatalf("regexp %s does not compile", s)
		}
		i.selectors = append(i.selectors, r)
	}
}

func (i *IntelMqHandler) matchSelectors(domain string) (match bool) {
	if i.selectors == nil {
		return true
	}
	for _, s := range i.selectors {
		if s.Match([]byte(domain)) {
			return true
		}
	}
	return false
}

// connect or reconnect TCP connection to IntelMQ TCP collector
func (i *IntelMqHandler) connect() {
	var err error
	if i.conn != nil {
		_ = i.conn.Close()
	}
	c := 0
	for {
		if c > intelMqConnectionAttempts {
			log.Fatalf("could not connect to IntelMQ collector after %d attempts", intelMqConnectionAttempts)
		}
		i.conn, err = net.DialTimeout("tcp", i.intelMqCollector, intelMqDialTimeout)
		if err == nil {
			break
		}
		if i.conn != nil {
			_ = i.conn.Close()
		}
		time.Sleep(intelMqDialTimeout)
		c += 1
	}
	if i.connWriter != nil {
		i.connWriter.Reset(i.conn)
	} else {
		i.connWriter = bufio.NewWriter(i.conn)
	}
}

// tcpWorker keeps a TCP connection to the IntelMQ collector open and sends observations
func (i *IntelMqHandler) tcpWorker() {
	stop := false
	i.connect()
	for {
		select {

		case o := <-i.observations:
			if !i.matchSelectors(o.Rrname) {
				continue
			}

			jsonObs, err := json.Marshal(o)
			if err != nil {
				log.Warnf("could not marshal observation due to %v", err)
			}
			b64Obs := base64.StdEncoding.EncodeToString(jsonObs)
			intelMqMessage := &IntelMqInput{
				ClassificationTaxonomy: "other",
				ClassificationType:     "unknown",
				FeedName:               i.intelMqFeedName,
				FeedProvider:           i.intelMqFeedProvider,
				TimeObservation:        time.Now().Format(time.RFC3339),
				TimeSource:             o.TimestampStart.Format(time.RFC3339),
				SourceFqdn:             o.Rrname,
				Raw:                    b64Obs,
			}
			jsonIntelMqMessage, err := json.Marshal(intelMqMessage)
			if err != nil {
				log.Warnf("could not marshal intelMqMessage due to %v", err)
			}

			msgLen := uint32(len(jsonIntelMqMessage))
			bLen := make([]byte, 4)
			binary.BigEndian.PutUint32(bLen, msgLen)
			_, err = i.connWriter.Write(bLen)
			if err != nil {
				log.Warnf("could not send observation length due to %v, reconnecting ...", err)
				i.observations <- o
				i.connect()
			}
			_, err = i.connWriter.Write(jsonIntelMqMessage)
			if err != nil {
				log.Warnf("could not send observation due to %v, reconnecting ...", err)
				i.observations <- o
				i.connect()
			}
		case <-time.After(intelMqTcpFlushTimeout):
			err := i.connWriter.Flush()
			if err != nil {
				log.Warnf("could not flush writer, reconnecting ...")
				i.connect()
			}
		case <-i.stopChan:
			stop = true
		}
		if stop {
			break
		}
	}
	close(i.stoppedChan)
}

func (i *IntelMqHandler) Stop() {
	close(i.stopChan)
	<-i.stoppedChan
}

func (i *IntelMqHandler) HandleObservations(o *observation.InputObservation) {
	select {
	case i.observations <- o:
	default:
		log.Warn("dropping observations")
	}
}

func (i *IntelMqHandler) HandleQuery(*db.QueryRequest, net.Conn) {
	log.Warn("the IntelMQ backend does not support queries ... ignoring")
}

func (i *IntelMqHandler) HandleDump(*db.DumpRequest, net.Conn) {
	log.Warn("the IntelMQ backend does not support dump requests ... ignoring")
}

func (i *IntelMqHandler) HandleBackup(*db.BackupRequest) {
	log.Warn("the IntelMQ backend does not support backup requests ... ignoring")
}
