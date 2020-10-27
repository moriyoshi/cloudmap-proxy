package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type ServiceUplooker interface {
	LookupService(ctx context.Context, namespaceName, serviceName string) (*ServiceDescriptor, error)
}

type ServiceInstance struct {
	InstanceId string
	Healthy    bool
	V4Addr     *net.IPAddr
	V6Addr     *net.IPAddr
	Attributes map[string]string
}

type ServiceDescriptor struct {
	NamespaceName string
	ServiceName   string
	Instances     []ServiceInstance
}

type targetAddr struct {
	namespaceName string
	serviceName   string
	prefNet       string
	net           string
	addr          string
	port          int
	count         int
}

type Server struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	su                   ServiceUplooker
	listenAddr           net.TCPAddr
	resolvedListenerAddr net.Addr
	targetAddr           targetAddr
	wg                   sync.WaitGroup
}

func (s *Server) Foo() {}

func getNetworkForIP(ip net.IP) string {
	if ip.To4() != nil {
		return "tcp4"
	} else if ip.To16() != nil {
		return "tcp6"
	}
	return ""
}

func NewServer(ctx context.Context, su ServiceUplooker, listenAddr net.TCPAddr, targetAddr string, connTimeout time.Duration) (s *Server, err error) {
	ctx, cancel := context.WithCancel(ctx)
	var lsnr net.Listener
	{
		lsnr, err = (&net.ListenConfig{}).Listen(ctx, getNetworkForIP(listenAddr.IP), listenAddr.String())
		if err != nil {
			err = fmt.Errorf("failed to listen on %s: %w", listenAddr.String(), err)
			return
		}
	}
	defer func() {
		if err != nil {
			lsnr.Close()
		}
	}()
	var intentionalCancel bool

	t, err := parseTargetAddr(targetAddr)
	if err != nil {
		err = fmt.Errorf("failed to parse target address %s: %w", targetAddr, err)
		return
	}

	s = &Server{
		ctx:                  ctx,
		cancel:               cancel,
		su:                   su,
		targetAddr:           t,
		resolvedListenerAddr: lsnr.Addr(),
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-ctx.Done()
		intentionalCancel = true
		lsnr.Close()
	}()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := lsnr.Accept()
			if err != nil {
				if !intentionalCancel {
					log.Error().Err(err).Send()
				}
				break
			}
			localAddr := conn.LocalAddr().String()
			remoteAddr := conn.RemoteAddr().String()
			log.Info().Str("local_addr", localAddr).Str("remote_addr", remoteAddr).Msg("server accepted new connection")
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handle(conn)
				log.Info().Str("local_addr", localAddr).Str("remote_addr", remoteAddr).Msg("connection closed")
			}()
		}
	}()
	return s, nil
}

func (s *Server) WaitForTermination() {
	s.wg.Wait()
}

func (s *Server) Close() {
	s.cancel()
}

func drainer(dest io.Writer, src io.Reader, bufsize int) error {
	buf := make([]byte, bufsize)
	for {
		nr, err := src.Read(buf)
		if err != nil {
			if err == io.EOF || os.IsTimeout(err) {
				return nil
			}
			return fmt.Errorf("failed to read from %+v: %w", src, err)
		}
		for b := buf[:nr]; len(b) > 0; {
			nw, err := dest.Write(b)
			if err != nil {
				if err == io.EOF || os.IsTimeout(err) {
					return nil
				}
				return fmt.Errorf("failed to write from %+v: %w", src, err)
			}
			b = b[nw:]
		}
	}
}

func parsePossibleSDAddr(a string) (sdAddr string, prefNet string) {
	if strings.HasPrefix(a, "aws-servicediscovery-v4:") {
		sdAddr = a[24:]
		prefNet = "tcp4"
	} else if strings.HasPrefix(a, "aws-servicediscovery-v6:") {
		sdAddr = a[24:]
		prefNet = "tcp6"
	} else if strings.HasPrefix(a, "aws-servicediscovery:") {
		sdAddr = a[21:]
	}
	return
}

func parseTargetAddr(a string) (target targetAddr, err error) {
	sdAddr, prefNet := parsePossibleSDAddr(a)
	if sdAddr != "" {
		addrParts := strings.SplitN(sdAddr, ":", 3)
		if len(addrParts) != 3 {
			err = fmt.Errorf("invalid address: %s", a)
			return
		}
		var port int
		port, err = strconv.Atoi(addrParts[2])
		if err != nil {
			err = fmt.Errorf("invalid port number: %s", addrParts[2])
			return
		}

		target.namespaceName = addrParts[0]
		target.serviceName = addrParts[1]
		target.prefNet = prefNet
		target.port = port
	} else {
		target.net = "tcp"
		target.addr = a
	}
	return
}

func getSuitableAddr(si *ServiceInstance, prefNet string, port int) (n string, a *net.TCPAddr) {
	if si.V4Addr != nil {
		if si.V6Addr != nil {
			switch prefNet {
			case "tcp4":
				n = "tcp4"
				a = &net.TCPAddr{
					IP:   si.V4Addr.IP,
					Port: port,
				}
			case "tcp6":
				n = "tcp6"
				a = &net.TCPAddr{
					IP:   si.V6Addr.IP,
					Port: port,
				}
			default:
				n = "tcp4"
				a = &net.TCPAddr{
					IP:   si.V4Addr.IP,
					Port: port,
				}
			}
		} else {
			n = "tcp4"
			a = &net.TCPAddr{
				IP:   si.V4Addr.IP,
				Port: port,
			}
		}
	} else if si.V6Addr != nil {
		n = "tcp6"
		a = &net.TCPAddr{
			IP:   si.V4Addr.IP,
			Port: port,
		}
	}
	return
}

func (t *targetAddr) resolve(su ServiceUplooker, ctx context.Context) (n string, a net.Addr, err error) {
	if t.namespaceName != "" {
		var sd *ServiceDescriptor
		sd, err = su.LookupService(ctx, t.namespaceName, t.serviceName)
		if err != nil {
			err = fmt.Errorf("failed to lookup service: %w", err)
			return
		}
		for i, _ := range sd.Instances {
			n, a = getSuitableAddr(
				&sd.Instances[(i+t.count)%len(sd.Instances)],
				t.prefNet,
				t.port,
			)
			if n != "" && a != nil {
				t.count = i + 1
				return
			}
		}
		err = fmt.Errorf("invalid response; neither v4 nor v6 address are available")
	} else {
		var _a *net.TCPAddr
		_a, err = net.ResolveTCPAddr(t.net, t.addr)
		if err != nil {
			n = getNetworkForIP(_a.IP)
		}
		a = _a
	}
	return
}

var longLongAgo = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)

func (s *Server) handle(conn net.Conn) {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	defer conn.Close()

	tgt := func() net.Conn {
		ctx, cancel := context.WithTimeout(ctx, connTimeout)
		defer cancel()
		n, a, err := s.targetAddr.resolve(s.su, ctx)
		if err != nil {
			log.Error().Err(err).Msg("failed to resolve target address")
			return nil
		}

		log.Info().Str("target", a.String()).Msg("connecting to target")

		tgt, err := (&net.Dialer{}).DialContext(ctx, n, a.String())
		if err != nil {
			log.Error().Err(err).Str("target", a.String()).Msgf("failed to dial to %s: %s", a.String(), err.Error())
			return nil
		}
		return tgt
	}()
	if tgt == nil {
		return
	}
	defer tgt.Close()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		if err := tgt.SetReadDeadline(longLongAgo); err != nil {
			log.Error().Err(err).Send()
		}
		if err := tgt.SetWriteDeadline(longLongAgo); err != nil {
			log.Error().Err(err).Send()
		}
		if err := conn.SetReadDeadline(longLongAgo); err != nil {
			log.Error().Err(err).Send()
		}
		if err := conn.SetWriteDeadline(longLongAgo); err != nil {
			log.Error().Err(err).Send()
		}
	}()
	go func() {
		defer wg.Done()
		err := drainer(conn, tgt, 131072)
		if err != nil {
			log.Error().Err(err).Send()
		}
		cancel()
	}()
	go func() {
		defer wg.Done()
		err := drainer(tgt, conn, 131072)
		if err != nil {
			log.Error().Err(err).Send()
		}
		cancel()
	}()
	wg.Wait()
}
