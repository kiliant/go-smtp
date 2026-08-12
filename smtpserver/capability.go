package smtpserver

import (
	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

type tlsAvailability uint8

const (
	tlsAny tlsAvailability = iota
	tlsBefore
	tlsAfter
)

type backendFeatureSet uint64

func (set backendFeatureSet) contains(required backendFeatureSet) bool {
	return set&required == required
}

type capabilityContext struct {
	mode          listenerMode
	tls           bool
	authenticated bool
	backend       backendFeatureSet
}

type capabilityDescriptor struct {
	keyword         smtp.Extension
	params          func(capabilityContext) string
	requiresBackend backendFeatureSet
	requiresTLS     tlsAvailability
	requiresAuth    bool
	modes           modeSet
	available       func(capabilityContext) bool
}

func computeCapabilities(ctx capabilityContext, descriptors []capabilityDescriptor) []smtpwire.Extension {
	extensions := make([]smtpwire.Extension, 0, len(descriptors))
	mode := modeSetSMTP
	if ctx.mode == modeLMTP {
		mode = modeSetLMTP
	}
	for _, descriptor := range descriptors {
		if descriptor.modes&mode == 0 || !ctx.backend.contains(descriptor.requiresBackend) {
			continue
		}
		if descriptor.requiresTLS == tlsBefore && ctx.tls || descriptor.requiresTLS == tlsAfter && !ctx.tls {
			continue
		}
		if descriptor.requiresAuth && !ctx.authenticated {
			continue
		}
		if descriptor.available != nil && !descriptor.available(ctx) {
			continue
		}
		extension := smtpwire.Extension{Keyword: string(descriptor.keyword)}
		if descriptor.params != nil {
			extension.Raw = descriptor.params(ctx)
		}
		extensions = append(extensions, extension)
	}
	return extensions
}
