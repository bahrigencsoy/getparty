package getparty

import (
	"fmt"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vbauerster/mpb/v5/decor"
)

type message struct {
	msg   string
	times int
	final bool
	done  chan struct{}
}

type msgGate struct {
	prefix string
	msgCh  chan *message
	done   chan struct{}
}

func newMsgGate(prefix string, quiet bool) msgGate {
	gate := msgGate{
		prefix: prefix,
		done:   make(chan struct{}),
	}
	if !quiet {
		gate.msgCh = make(chan *message, 4)
	}
	return gate
}

func (s msgGate) flash(msg *message) {
	msg.times = 14
	msg.msg = fmt.Sprintf("%s:%s", s.prefix, msg.msg)
	select {
	case s.msgCh <- msg:
	case <-s.done:
		if msg.final && msg.done != nil {
			close(msg.done)
		}
	}
}

type mainDecorator struct {
	decor.WC
	curTry   *uint32
	name     string
	format   string
	flashMsg *message
	messages []*message
	gate     msgGate
}

func newMainDecorator(curTry *uint32, format, name string, gate msgGate, wc decor.WC) decor.Decorator {
	d := &mainDecorator{
		WC:     wc.Init(),
		curTry: curTry,
		name:   name,
		format: format,
		gate:   gate,
	}
	return d
}

func (d *mainDecorator) depleteMessages() {
	for {
		select {
		case m := <-d.gate.msgCh:
			d.messages = append(d.messages, m)
		default:
			return
		}
	}
}

func (d *mainDecorator) Decor(stat decor.Statistics) string {
	if !stat.Completed && d.flashMsg != nil {
		m := d.flashMsg.msg
		if d.flashMsg.times > 0 {
			d.flashMsg.times--
		} else if d.flashMsg.final {
			if d.flashMsg.done != nil {
				close(d.flashMsg.done)
				d.flashMsg.done = nil
			}
		} else {
			d.flashMsg = nil
		}
		return d.FormatMsg(m)
	}

	d.depleteMessages()

	if len(d.messages) > 0 {
		d.flashMsg = d.messages[0]
		copy(d.messages, d.messages[1:])
		d.messages = d.messages[:len(d.messages)-1]
		d.flashMsg.times--
		return d.FormatMsg(d.flashMsg.msg)
	}

	name := d.name
	if atomic.LoadUint32(&globTry) > 0 {
		name = fmt.Sprintf("%s:R%02d", name, atomic.LoadUint32(d.curTry))
	}
	return d.FormatMsg(fmt.Sprintf(d.format, name, decor.SizeB1024(stat.Total)))
}

func (d *mainDecorator) Shutdown() {
	close(d.gate.done)
}

type peak struct {
	decor.WC
	name          string
	format        string
	msg           string
	byteAcc       int64
	durAcc        int64
	minDurPerByte int64
	once          sync.Once
}

func newSpeedPeak(name, format string, wc decor.WC) decor.Decorator {
	d := &peak{
		WC:            wc.Init(),
		name:          name,
		format:        format,
		minDurPerByte: math.MaxInt64,
	}
	return d
}

func (s *peak) EwmaUpdate(n int64, dur time.Duration) {
	s.byteAcc += n
	s.durAcc += int64(dur)
	if s.byteAcc >= 1024*64 {
		durPerByte := s.durAcc / s.byteAcc
		if durPerByte < s.minDurPerByte {
			s.minDurPerByte = durPerByte
		}
		fmt.Fprintf(os.Stderr, "%s: byteAcc=%d durAcc=%d speed=%v max=%v\n",
			s.name,
			s.byteAcc,
			s.durAcc,
			decor.FmtAsSpeed(decor.SizeB1024(1e9/durPerByte)),
			decor.FmtAsSpeed(decor.SizeB1024(1e9/s.minDurPerByte)),
		)
		s.byteAcc, s.durAcc = 0, 0
	}
}

func (s *peak) onComplete() {
	if s.byteAcc > 0 {
		durPerByte := s.durAcc / s.byteAcc
		if durPerByte < s.minDurPerByte {
			s.minDurPerByte = durPerByte
		}
		fmt.Fprintf(os.Stderr, "%s: onComplete: byteAcc=%d durAcc=%d speed=%v max=%v\n",
			s.name,
			s.byteAcc,
			s.durAcc,
			decor.FmtAsSpeed(decor.SizeB1024(1e9/durPerByte)),
			decor.FmtAsSpeed(decor.SizeB1024(1e9/s.minDurPerByte)),
		)
	}
	if s.minDurPerByte == 0 {
		s.msg = fmt.Sprintf(s.format, decor.FmtAsSpeed(decor.SizeB1024(0)))
	} else {
		s.msg = fmt.Sprintf(s.format, decor.FmtAsSpeed(decor.SizeB1024(1e9/s.minDurPerByte)))
	}
}

func (s *peak) Decor(stat decor.Statistics) string {
	if stat.Completed {
		s.once.Do(s.onComplete)
	}
	return s.FormatMsg(s.msg)
}
