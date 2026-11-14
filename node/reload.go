package node

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/shmily2-1/V2bX-2/api/panel"
	vCore "github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
)

// Caller holds stateMu. Restore the previous node when replacement cannot start.
func (c *Controller) replaceNode(info *panel.NodeInfo, users []panel.UserInfo, alive map[int]int) error {
	coreType := c.Core
	if coreType == "" {
		coreType = c.server.Type()
	}
	if err := vCore.ValidateNode(coreType, info, c.Options); err != nil {
		return err
	}
	for _, rule := range info.Rules.Regexp {
		if _, err := regexp.Compile(rule); err != nil {
			return err
		}
	}
	if info.Security == panel.Tls {
		if err := c.requestCert(); err != nil {
			return err
		}
	}
	if c.nodeAdded {
		if _, err := c.collectTraffic(); err != nil {
			return err
		}
	}
	oldInfo, oldUsers, oldTag := c.info, c.userList, c.tag
	newTag := c.Name
	if newTag == "" {
		newTag = c.buildNodeTag(info)
	}
	if c.nodeAdded {
		if err := c.server.DelNode(oldTag); err != nil {
			return err
		}
		c.nodeAdded = false
	}
	install := func(tag string, n *panel.NodeInfo, u []panel.UserInfo) error {
		l := limiter.AddLimiter(tag, &c.LimitConfig, u, alive)
		if err := l.UpdateRule(&n.Rules); err != nil {
			return err
		}
		if err := c.server.AddNode(tag, n, c.Options); err != nil {
			return err
		}
		if len(u) > 0 {
			if _, err := c.server.AddUsers(&vCore.AddUsersParams{Tag: tag, Users: u, NodeInfo: n}); err != nil {
				return errors.Join(err, c.server.DelNode(tag))
			}
		}
		c.limiter = l
		c.nodeAdded = true
		return nil
	}
	if err := install(newTag, info, users); err != nil {
		if newTag != oldTag {
			limiter.DeleteLimiter(newTag)
		}
		if restoreErr := install(oldTag, oldInfo, oldUsers); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore previous node: %w", restoreErr))
		}
		return err
	}
	if oldTag != newTag {
		limiter.DeleteLimiter(oldTag)
	}
	c.info, c.userList, c.tag = info, users, newTag
	c.traffic = make(map[string]int64)
	c.nodeInfoMonitorPeriodic.SetInterval(info.PullInterval)
	c.userReportPeriodic.SetInterval(info.PushInterval)
	return nil
}
