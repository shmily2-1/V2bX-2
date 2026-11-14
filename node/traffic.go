package node

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/shmily2-1/V2bX-2/api/panel"
)

type trafficState struct {
	Version  int                 `json:"version"`
	Pending  []panel.UserTraffic `json:"pending"`
	Inflight []panel.UserTraffic `json:"inflight,omitempty"`
	BatchID  string              `json:"batch_id,omitempty"`
}

// Caller holds stateMu. The filename is stable across display-name changes.
func (c *Controller) loadTrafficState() error {
	c.pendingTraffic = make(map[int]panel.UserTraffic)
	if c.TrafficStateDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.TrafficStateDir, 0700); err != nil {
		return err
	}
	identity, err := json.Marshal([]any{c.apiClient.APIHost, c.apiClient.NodeType, c.apiClient.NodeId})
	if err != nil {
		return err
	}
	key := sha256.Sum256(identity)
	c.trafficStatePath = filepath.Join(c.TrafficStateDir, hex.EncodeToString(key[:])+".json")
	data, err := os.ReadFile(c.trafficStatePath)
	if os.IsNotExist(err) {
		return c.saveTrafficState()
	}
	if err != nil {
		return err
	}
	var state trafficState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("invalid traffic state: %w", err)
	}
	if state.Version != 1 {
		return fmt.Errorf("unsupported traffic state version %d", state.Version)
	}
	for _, item := range state.Pending {
		if item.UID <= 0 || item.Upload < 0 || item.Download < 0 {
			return fmt.Errorf("invalid pending traffic")
		}
		if _, exists := c.pendingTraffic[item.UID]; exists {
			return fmt.Errorf("duplicate pending traffic UID")
		}
		c.pendingTraffic[item.UID] = item
	}
	if (state.BatchID == "") != (len(state.Inflight) == 0) {
		return fmt.Errorf("invalid inflight traffic state")
	}
	seen := map[int]bool{}
	for _, item := range state.Inflight {
		pending := c.pendingTraffic[item.UID]
		if item.UID <= 0 || seen[item.UID] || item.Upload < 0 || item.Download < 0 || item.Upload > pending.Upload || item.Download > pending.Download {
			return fmt.Errorf("invalid inflight traffic counts")
		}
		seen[item.UID] = true
	}
	c.inflightID, c.inflightTraffic = state.BatchID, state.Inflight
	return nil
}

func (c *Controller) trafficBatch() []panel.UserTraffic {
	items := make([]panel.UserTraffic, 0, len(c.pendingTraffic))
	for _, item := range c.pendingTraffic {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UID < items[j].UID })
	return items
}

func (c *Controller) saveTrafficState() error {
	if c.trafficStatePath == "" {
		return nil
	}
	data, err := json.Marshal(trafficState{Version: 1, Pending: c.trafficBatch(), Inflight: c.inflightTraffic, BatchID: c.inflightID})
	if err != nil {
		return err
	}
	return writePrivateFile(c.trafficStatePath, data)
}

func (c *Controller) prepareTrafficBatch(batch []panel.UserTraffic) ([]panel.UserTraffic, error) {
	if c.inflightID != "" {
		if err := c.saveTrafficState(); err != nil {
			return nil, err
		}
		return c.inflightTraffic, nil
	}
	if c.info == nil || c.info.Common == nil || !c.info.Common.TrafficBatch || len(batch) == 0 {
		return batch, nil
	}
	if c.trafficStatePath == "" {
		return nil, fmt.Errorf("batch reporting requires TrafficStateDir")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	c.inflightID = hex.EncodeToString(id[:])
	c.inflightTraffic = append([]panel.UserTraffic(nil), batch...)
	if err := c.saveTrafficState(); err != nil {
		return nil, err
	}
	return c.inflightTraffic, nil
}

func (c *Controller) collectTraffic() ([]panel.UserTraffic, error) {
	items, err := c.server.GetUserTrafficSlice(c.tag, true)
	if err != nil {
		return nil, fmt.Errorf("collect traffic: %w", err)
	}
	if c.pendingTraffic == nil {
		c.pendingTraffic = make(map[int]panel.UserTraffic)
	}
	if c.LimitConfig.EnableDynamicSpeedLimit && c.traffic == nil {
		c.traffic = make(map[string]int64)
	}
	for _, item := range items {
		current := c.pendingTraffic[item.UID]
		current.UID = item.UID
		current.Upload += item.Upload
		current.Download += item.Download
		c.pendingTraffic[item.UID] = current
		if c.LimitConfig.EnableDynamicSpeedLimit {
			for _, user := range c.userList {
				if user.Id == item.UID {
					c.traffic[user.Uuid] += item.Upload + item.Download
					break
				}
			}
		}
	}
	// Persist before sending. Retries reuse these counts instead of draining them twice.
	if err := c.saveTrafficState(); err != nil {
		return items, fmt.Errorf("save pending traffic: %w", err)
	}
	return items, nil
}
