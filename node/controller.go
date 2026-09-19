package node

import (
	"fmt"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/common/task"
	"github.com/shmily2-1/V2bX-2/conf"
	vCore "github.com/shmily2-1/V2bX-2/core"
	"github.com/shmily2-1/V2bX-2/limiter"
	log "github.com/sirupsen/logrus"
)

type Controller struct {
	server                    vCore.Core
	apiClient                 *panel.Client
	tag                       string
	limiter                   *limiter.Limiter
	traffic                   map[string]int64
	userList                  []panel.UserInfo
	aliveMap                  map[int]int
	info                      *panel.NodeInfo
	nodeInfoMonitorPeriodic   *task.Task
	userReportPeriodic        *task.Task
	renewCertPeriodic         *task.Task
	dynamicSpeedLimitPeriodic *task.Task
	onlineIpReportPeriodic    *task.Task
	*conf.Options
}

// NewController return a Node controller with default parameters.
func NewController(server vCore.Core, api *panel.Client, config *conf.Options) *Controller {
	controller := &Controller{
		server:    server,
		Options:   config,
		apiClient: api,
	}
	return controller
}

// Start implement the Start() function of the service interface
func (c *Controller) Start() error {
	// First fetch Node Info
	var err error
	node, err := c.apiClient.GetNodeInfo()
	if err != nil {
		return fmt.Errorf("get node info error: %s", err)
	}
	// Update user
	c.userList, err = c.apiClient.GetUserList()
	if err != nil {
		return fmt.Errorf("get user list error: %s", err)
	}
	if len(c.userList) == 0 {
		if node.Type != "hysteria2" {
			return fmt.Errorf("no authorized users; waiting mode is only verified for hysteria2")
		}
		log.Warn("No authorized users; starting a deny-all node and waiting for panel assignments")
	}
	c.aliveMap, err = c.apiClient.GetUserAlive()
	if err != nil {
		return fmt.Errorf("failed to get user alive list: %s", err)
	}
	if len(c.Options.Name) == 0 {
		c.tag = c.buildNodeTag(node)
	} else {
		c.tag = c.Options.Name
	}

	// add limiter
	l := limiter.AddLimiter(c.tag, &c.LimitConfig, c.userList, c.aliveMap)
	// add rule limiter
	if err = l.UpdateRule(&node.Rules); err != nil {
		return fmt.Errorf("update rule error: %s", err)
	}
	c.limiter = l
	if node.Security == panel.Tls {
		err = c.requestCert()
		if err != nil {
			return fmt.Errorf("request cert error: %s", err)
		}
	}
	// Add new tag
	err = c.server.AddNode(c.tag, node, c.Options)
	if err != nil {
		return fmt.Errorf("add new node error: %s", err)
	}
	added, err := c.addAuthorizedUsers(node)
	if err != nil {
		return fmt.Errorf("add users error: %s", err)
	}
	log.WithField("tag", c.tag).Infof("Added %d new users", added)
	c.info = node
	c.startTasks(node)
	return nil
}

// Empty panel lists are valid, but must never enable unauthenticated access.
func (c *Controller) addAuthorizedUsers(node *panel.NodeInfo) (int, error) {
	if len(c.userList) == 0 {
		return 0, nil
	}
	return c.server.AddUsers(&vCore.AddUsersParams{
		Tag:      c.tag,
		Users:    c.userList,
		NodeInfo: node,
	})
}

// Close implement the Close() function of the service interface
func (c *Controller) Close() error {
	limiter.DeleteLimiter(c.tag)
	if c.nodeInfoMonitorPeriodic != nil {
		c.nodeInfoMonitorPeriodic.Close()
	}
	if c.userReportPeriodic != nil {
		c.userReportPeriodic.Close()
	}
	if c.renewCertPeriodic != nil {
		c.renewCertPeriodic.Close()
	}
	if c.dynamicSpeedLimitPeriodic != nil {
		c.dynamicSpeedLimitPeriodic.Close()
	}
	if c.onlineIpReportPeriodic != nil {
		c.onlineIpReportPeriodic.Close()
	}
	err := c.server.DelNode(c.tag)
	if err != nil {
		return fmt.Errorf("del node error: %s", err)
	}
	return nil
}

func (c *Controller) buildNodeTag(node *panel.NodeInfo) string {
	return fmt.Sprintf("[%s]-%s:%d", c.apiClient.APIHost, node.Type, node.Id)
}
