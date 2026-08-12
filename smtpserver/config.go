package smtpserver

import (
	"errors"
	"net"
	"strconv"
)

type constructionConfig struct {
	listener            net.Listener
	mode                listenerMode
	backendNewSession   bool
	chunking            bool
	binaryMIME          bool
	maxSpoolBytes       int64
	maxSpoolMemoryBytes int64
	maxTotalSpoolBytes  int64
	maxTotalSpoolMemory int64
	maxConcurrentSpools int
	maxConnections      int
}

func validateConstruction(config constructionConfig) error {
	var problems []string
	if config.listener == nil {
		problems = append(problems, "Listener is required")
	}
	if !config.backendNewSession {
		problems = append(problems, "Backend.NewSession is required")
	}
	if config.mode == modeLMTP && config.listener != nil && listenerPort(config.listener.Addr()) == 25 {
		problems = append(problems, "LMTP must not use TCP port 25")
	}
	if config.binaryMIME && !config.chunking {
		problems = append(problems, "BINARYMIME requires CHUNKING")
	}
	if config.chunking {
		if config.maxSpoolBytes <= 0 {
			problems = append(problems, "MaxSpoolBytes must be positive when CHUNKING is enabled")
		}
		if config.maxSpoolMemoryBytes <= 0 {
			problems = append(problems, "MaxSpoolMemoryBytes must be positive when CHUNKING is enabled")
		}
		if config.maxTotalSpoolBytes <= 0 {
			problems = append(problems, "MaxTotalSpoolBytes must be positive when CHUNKING is enabled")
		}
		if config.maxTotalSpoolMemory <= 0 {
			problems = append(problems, "MaxTotalSpoolMemoryBytes must be positive when CHUNKING is enabled")
		}
		if config.maxConcurrentSpools <= 0 {
			problems = append(problems, "MaxConcurrentSpools must be positive when CHUNKING is enabled")
		}
	}
	if len(problems) != 0 {
		return errors.New("smtpserver: invalid server options: " + joinProblems(problems))
	}
	return nil
}

func listenerPort(addr net.Addr) int {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.Port
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return 0
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return value
}
