package privatedns

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

type server struct {
	udp, tcp *dns.Server
	mu       sync.Mutex
	err      error
}

func serve(address string, h dns.Handler) (*server, error) {
	u, e := net.ListenPacket("udp", address)
	if e != nil {
		return nil, e
	}
	// Port zero is useful for unprivileged tests; UDP and TCP share the port.
	t, e := net.Listen("tcp", u.LocalAddr().String())
	if e != nil {
		u.Close()
		return nil, e
	}
	s := &server{}
	slots := make(chan struct{}, 128)
	bounded := dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			h.ServeDNS(w, q)
		default:
			replyCode(w, q, dns.RcodeServerFailure)
		}
	})
	ready := make(chan struct{}, 2)
	s.udp = &dns.Server{PacketConn: u, Handler: bounded, UDPSize: 1232, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, NotifyStartedFunc: func() { ready <- struct{}{} }}
	s.tcp = &dns.Server{Listener: t, Handler: bounded, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: func() time.Duration { return 5 * time.Second }, MaxTCPQueries: 16, NotifyStartedFunc: func() { ready <- struct{}{} }}
	for _, d := range []*dns.Server{s.udp, s.tcp} {
		go func(d *dns.Server) {
			e := d.ActivateAndServe()
			if e != nil {
				s.mu.Lock()
				s.err = e
				s.mu.Unlock()
			}
		}(d)
	}
	<-ready
	<-ready
	return s, nil
}
func (s *server) health() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *server) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	return errors.Join(s.udp.ShutdownContext(ctx), s.tcp.ShutdownContext(ctx))
}

type forwarder struct {
	suspended     atomic.Bool
	zone          *authority
	privateServer string
	upstreams     []string
}

func (f *forwarder) ServeDNS(w dns.ResponseWriter, q *dns.Msg) {
	if len(q.Question) != 1 || q.Opcode != dns.OpcodeQuery {
		replyCode(w, q, dns.RcodeFormatError)
		return
	}
	z := f.zone.current.Load()
	if z == nil {
		replyCode(w, q, dns.RcodeServerFailure)
		return
	}
	servers := f.upstreams
	if z.private(q.Question[0].Name) {
		if f.suspended.Load() {
			replyCode(w, q, dns.RcodeServerFailure)
			return
		}
		servers = []string{f.privateServer}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, address := range servers {
		transport := "udp"
		if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
			transport = "tcp"
		}
		c := &dns.Client{Net: transport, Timeout: 2 * time.Second}
		r, _, e := c.ExchangeContext(ctx, q, address)
		if e == nil && r.Truncated && transport == "udp" {
			c.Net = "tcp"
			r, _, e = c.ExchangeContext(ctx, q, address)
		}
		if e == nil && r.Response && len(r.Question) == 1 && r.Question[0] == q.Question[0] {
			if r.Rcode == dns.RcodeServerFailure {
				continue
			}
			r.RecursionAvailable = true
			writeReply(w, q, r)
			return
		}
	}
	replyCode(w, q, dns.RcodeServerFailure)
}
