package backend

import (
	"bufio"
	"encoding/json"
	"net"
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

type IntelMqHandler struct {
	intelMqCollector string // hostname:port of IntelMQ TCP collector
	stopChan         chan bool
	stoppedChan      chan bool
	observations     chan *observation.InputObservation
	conn             net.Conn
	connWriter       *bufio.Writer
}

func NewIntelMqHandler(intelMqCollector string) *IntelMqHandler {
	i := &IntelMqHandler{intelMqCollector: intelMqCollector}
	i.stopChan = make(chan bool)
	i.stoppedChan = make(chan bool)
	i.observations = make(chan *observation.InputObservation, intelMqObservationsBufferSize)
	go i.tcpWorker()
	return i
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
	counter := 0
	for {
		select {
		case o := <-i.observations:
			jsonObs, err := json.Marshal(o)
			if err != nil {
				log.Warnf("could not marshal observation due to %v", err)
			}
			_, err = i.connWriter.Write(jsonObs)
			if err != nil {
				log.Warnf("could not send observation due to %v, reconnecting ...", err)
				i.observations <- o
				i.connect()
			}
			counter += 1
		case <-time.After(intelMqTcpFlushTimeout):
			err := i.connWriter.Flush()
			if err != nil {
				log.Warnf("could not flush writer, reconnecting ...")
				i.connect()
			}
			log.Printf("sent %d", counter)
		case <-i.stopChan:
			stop = true
		}
		if stop {
			break
		}
	}
	close(i.stoppedChan)
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
