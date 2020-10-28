package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const envNamePrefixEndpointOverride = "AWS_ENDPOINT_OVERRIDES_"

var rootCtx = context.Background()

var debugLevel int = 0
var connTimeout time.Duration
var cacheTtl time.Duration

func populateConfigFromEnvVars() {
	{
		v, err := strconv.Atoi(os.Getenv("CLOUDMAP_PROXY_DEBUG"))
		if err == nil {
			debugLevel = v
		}
	}
	{
		v, err := time.ParseDuration(os.Getenv("CLOUDMAP_PROXY_CONN_TIMEOUT"))
		if err == nil {
			connTimeout = v
		}
	}
	{
		v, err := time.ParseDuration(os.Getenv("CLOUDMAP_PROXY_CACHE_TTL"))
		if err == nil {
			cacheTtl = v
		}
	}
}

func parseListenAddr(addr string) (listenAddr net.TCPAddr, err error) {
	if addr[0] == ':' {
		listenAddr.IP = make(net.IP, 4)
		listenAddr.Port, err = strconv.Atoi(addr[1:])
		if err != nil {
			err = fmt.Errorf("fail to parse listen address", err)
		}
	} else {
		var _listenAddr *net.TCPAddr
		_listenAddr, err = net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			err = fmt.Errorf("fail to resolve listen address: %w", err)
		}
		listenAddr = *_listenAddr
	}
	return
}

func main() {
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()

	var debug bool
	{
		flag.BoolVar(&debug, "debug", false, "debug mode")
		flag.DurationVar(&connTimeout, "conn-timeout", 10*time.Second, "connection timeout")
		flag.DurationVar(&cacheTtl, "cache-ttl", 60*time.Second, "io timeout")
	}
	populateConfigFromEnvVars()
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "%s: too few arguments\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(255)
	}

	if debug {
		debugLevel = 2
	}

	if debugLevel > 0 {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	sess, err := getAwsSession()
	if err != nil {
		log.Error().Err(err).Msg("fail to fetch aws configuration")
		os.Exit(1)
	}

	if len(args[1]) == 0 {
		log.Error().Err(err).Msg("listen address must be specified")
		os.Exit(1)
	}

	listenAddr, err := parseListenAddr(args[1])
	if err != nil {
		log.Error().Err(err).Msg(err.Error())
		os.Exit(1)
	}

	s, err := NewServer(
		ctx,
		NewUplookerCache(
			NewCloudMapServiceUplooker(sess),
			cacheTtl,
		), listenAddr,
		args[0],
		connTimeout,
	)
	if err != nil {
		log.Error().Err(err).Msg("fail to resolve listen address")
		os.Exit(1)
	}
	{
		sigCh := make(chan os.Signal)
		go func() {
		outer:
			for {
				select {
				case <-ctx.Done():
					break outer
				case <-sigCh:
					log.Info().Msg("shutdown initiated")
					s.Close()
					break outer
				}
			}
		}()
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	}
	log.Info().Msgf("cloudmap proxy started listening on %s", listenAddr.String())
	s.WaitForTermination()
	log.Info().Msg("cloudmap proxy stopped")
}
