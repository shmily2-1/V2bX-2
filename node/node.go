package node

import (
	"errors"
	"fmt"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/conf"
	vCore "github.com/shmily2-1/V2bX-2/core"
)

type Node struct {
	controllers []*Controller
}

func New() *Node {
	return &Node{}
}

func (n *Node) Start(nodes []conf.NodeConfig, core vCore.Core) error {
	n.controllers = nil
	for i := range nodes {
		p, err := panel.New(&nodes[i].ApiConfig)
		if err != nil {
			return errors.Join(err, n.Close())
		}
		// Register controller service
		controller := NewController(core, p, &nodes[i].Options)
		err = controller.Start()
		if err != nil {
			startErr := fmt.Errorf("start node controller [%s-%s-%d] error: %s",
				nodes[i].ApiConfig.APIHost,
				nodes[i].ApiConfig.NodeType,
				nodes[i].ApiConfig.NodeID,
				err)
			return errors.Join(startErr, n.Close())
		}
		n.controllers = append(n.controllers, controller)
	}
	return nil
}

func (n *Node) Close() error {
	var errs []error
	for _, c := range n.controllers {
		err := c.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}
	n.controllers = nil
	return errors.Join(errs...)
}
