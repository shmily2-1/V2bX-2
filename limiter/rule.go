package limiter

import (
	"regexp"

	"github.com/shmily2-1/V2bX-2/api/panel"
)

func (l *Limiter) CheckDomainRule(destination string) (reject bool) {
	l.access.RLock()
	defer l.access.RUnlock()
	// have rule
	for i := range l.DomainRules {
		if l.DomainRules[i].MatchString(destination) {
			reject = true
			break
		}
	}
	return
}

func (l *Limiter) CheckProtocolRule(protocol string) (reject bool) {
	l.access.RLock()
	defer l.access.RUnlock()
	for i := range l.ProtocolRules {
		if l.ProtocolRules[i] == protocol {
			reject = true
			break
		}
	}
	return
}

func (l *Limiter) UpdateRule(rule *panel.Rules) error {
	domains := make([]*regexp.Regexp, len(rule.Regexp))
	for i := range rule.Regexp {
		var err error
		domains[i], err = regexp.Compile(rule.Regexp[i])
		if err != nil {
			return err
		}
	}
	l.access.Lock()
	defer l.access.Unlock()
	l.DomainRules = domains
	l.ProtocolRules = append([]string(nil), rule.Protocol...)
	return nil
}
