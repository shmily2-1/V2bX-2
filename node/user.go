package node

import (
	"github.com/shmily2-1/V2bX-2/api/panel"
	log "github.com/sirupsen/logrus"
)

func (c *Controller) reportUserTrafficTask() (err error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.closed {
		return nil
	}
	userTraffic, collectErr := c.collectTraffic()
	if collectErr != nil {
		log.WithError(collectErr).Error("Collect/persist traffic failed; retained pending counts")
		return nil
	}
	batch := c.trafficBatch()
	toReport := batch[:0]
	for _, item := range batch {
		if item.Upload+item.Download > c.ReportMinTraffic*1024 {
			toReport = append(toReport, item)
		}
	}
	batch = toReport
	batch, err = c.prepareTrafficBatch(batch)
	if err != nil {
		return err
	}
	if len(batch) > 0 {
		err = c.apiClient.ReportTrafficBatch(batch, c.inflightID)
		if err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Info("Report user traffic failed")
		} else {
			for _, item := range batch {
				remaining := c.pendingTraffic[item.UID]
				remaining.Upload -= item.Upload
				remaining.Download -= item.Download
				if remaining.Upload == 0 && remaining.Download == 0 {
					delete(c.pendingTraffic, item.UID)
				} else {
					c.pendingTraffic[item.UID] = remaining
				}
			}
			c.inflightID, c.inflightTraffic = "", nil
			if err := c.saveTrafficState(); err != nil {
				log.WithError(err).Error("Persist traffic acknowledgement failed; disk state may be retried after restart")
			}
			log.WithField("tag", c.tag).Infof("Report %d users traffic", len(batch))
		}
	}

	if onlineDevice, err := c.limiter.GetOnlineDevice(); err != nil {
		log.Print(err)
	} else if len(*onlineDevice) > 0 {
		// Only report user has traffic > 100kb to allow ping test
		var result []panel.OnlineUser
		var nocountUID = make(map[int]struct{})
		for _, traffic := range userTraffic {
			total := traffic.Upload + traffic.Download
			if total < int64(c.Options.DeviceOnlineMinTraffic*1000) {
				nocountUID[traffic.UID] = struct{}{}
			}
		}
		for _, online := range *onlineDevice {
			if _, ok := nocountUID[online.UID]; !ok {
				result = append(result, online)
			}
		}
		data := make(map[int][]string)
		for _, onlineuser := range result {
			// json structure: { UID1:["ip1","ip2"],UID2:["ip3","ip4"] }
			data[onlineuser.UID] = append(data[onlineuser.UID], onlineuser.IP)
		}
		if err = c.apiClient.ReportNodeOnlineUsers(&data); err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Info("Report online users failed")
		} else {
			log.WithField("tag", c.tag).Infof("Total %d online users, %d Reported", len(*onlineDevice), len(result))
			log.WithField("tag", c.tag).Debugf("Online users: %+v", data)
		}
	}

	userTraffic = nil
	return nil
}

func compareUserList(old, new []panel.UserInfo) (deleted, added []panel.UserInfo) {
	oldMap := make(map[string]panel.UserInfo)
	for _, user := range old {
		oldMap[user.Uuid] = user
	}

	for _, user := range new {
		previous, exists := oldMap[user.Uuid]
		if !exists {
			added = append(added, user)
		} else {
			if previous != user {
				deleted = append(deleted, previous)
				added = append(added, user)
			}
			delete(oldMap, user.Uuid)
		}
	}

	for _, user := range oldMap {
		deleted = append(deleted, user)
	}

	return deleted, added
}
