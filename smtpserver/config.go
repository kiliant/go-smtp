package smtpserver

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
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
	greetingIdentity    string
	commandTimeout      time.Duration
	dataTimeout         time.Duration
	maxMessageBytes     int64
	maxRecipients       int
	maxTransactions     int
	authBefore          []string
	authAfter           []string
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
	if !validGreetingIdentity(config.greetingIdentity) {
		problems = append(problems, "GreetingIdentity must be a domain or address-literal without whitespace")
	}
	if config.commandTimeout <= 0 {
		problems = append(problems, "CommandTimeout must be positive")
	}
	if config.dataTimeout <= 0 {
		problems = append(problems, "DataTimeout must be positive")
	}
	if config.maxMessageBytes <= 0 {
		problems = append(problems, "MaxMessageBytes must be positive")
	}
	if config.maxRecipients < 100 {
		problems = append(problems, "MaxRecipients must be at least 100")
	}
	if config.maxTransactions < 0 || config.maxTransactions > 999999 {
		problems = append(problems, "MaxTransactions must be between 0 and 999999")
	}
	problems = append(problems, validateAuthMechanisms("AuthMechanismsBeforeTLS", config.authBefore)...)
	problems = append(problems, validateAuthMechanisms("AuthMechanismsAfterTLS", config.authAfter)...)
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

func validateAuthMechanisms(field string, mechanisms []string) []string {
	seen := make(map[string]bool)
	var problems []string
	for i, mechanism := range mechanisms {
		name := strings.ToUpper(mechanism)
		if name == "" {
			problems = append(problems, field+" contains an empty mechanism")
			continue
		}
		for _, c := range name {
			if (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				problems = append(problems, field+" contains an invalid mechanism at index "+strconv.Itoa(i))
				break
			}
		}
		if seen[name] {
			problems = append(problems, field+" contains duplicate mechanism "+name)
		}
		seen[name] = true
		mechanisms[i] = name
	}
	return problems
}

func validateSession(session *Session) error {
	if session == nil {
		return errors.New("smtpserver: invalid backend session: Session is nil")
	}
	var problems []string
	if session.Mail == nil {
		problems = append(problems, "Session.Mail is required")
	}
	if session.Rcpt == nil {
		problems = append(problems, "Session.Rcpt is required")
	}
	if session.Data == nil {
		problems = append(problems, "Session.Data is required")
	}
	if session.Reset == nil {
		problems = append(problems, "Session.Reset is required")
	}
	if session.Close == nil {
		problems = append(problems, "Session.Close is required")
	}
	if (session.Authenticate != nil || session.ChallengeResponse != nil || session.SCRAMCredentials != nil) && session.CommitAuth == nil {
		problems = append(problems, "Session.CommitAuth is required when authentication verification is configured")
	}
	problems = append(problems, validateExtensionSession(session)...)
	if len(problems) != 0 {
		return errors.New("smtpserver: invalid backend session: " + joinProblems(problems))
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
