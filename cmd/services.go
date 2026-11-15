package cmd

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/localservice"
	"github.com/spf13/cobra"
)

func disabledServicesCommand() {
	var configFile, id, file, kind, output, clientKey string
	var uid int
	root := &cobra.Command{Use: "services", Short: "管理本地服务与 WireGuard Peer"}
	root.PersistentFlags().StringVarP(&configFile, "config", "c", "/etc/V2bX/config.json", "主配置路径")
	root.PersistentFlags().StringVar(&id, "id", "", "服务 ID")
	load := func() (*conf.Conf, error) { c := conf.New(); return c, c.LoadFromPath(configFile) }
	read := func(path string, target any) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, target); err != nil {
			return fmt.Errorf("输入 JSON 无效")
		}
		return nil
	}
	list := &cobra.Command{Use: "list", Short: "查看配置与真实运行状态", RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		specs, err := localservice.LoadSpecs(c)
		if err != nil {
			return err
		}
		status := map[string]*localservice.Status{}
		data, readErr := os.ReadFile(filepath.Join(c.LocalServiceDir, ".state", "status.json"))
		if readErr == nil {
			if err = json.Unmarshal(data, &status); err != nil {
				return fmt.Errorf("服务状态文件无效")
			}
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		var rows []any
		for _, s := range specs {
			if (id != "" && s.ID != id) || (kind != "" && s.Type != kind) {
				continue
			}
			st := status[s.ID]
			if st != nil && time.Since(st.Updated) > 5*time.Second {
				copy := *st
				copy.State = "状态过期"
				st = &copy
			}
			rows = append(rows, map[string]any{"配置": localservice.Redacted(s), "状态": st})
		}
		if rows == nil {
			rows = []any{}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
	}}
	list.Flags().StringVar(&kind, "type", "", "按服务类型筛选")
	root.AddCommand(list)
	put := &cobra.Command{Use: "put", Short: "从 JSON 添加或替换服务配置", RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		var s conf.LocalService
		if err = read(file, &s); err != nil {
			return err
		}
		return localservice.Edit(c, s.ID, func(target *conf.LocalService) error { *target = s; return nil })
	}}
	put.Flags().StringVarP(&file, "file", "f", "", "服务 JSON 文件")
	_ = put.MarkFlagRequired("file")
	root.AddCommand(put)
	for _, action := range []string{"enable", "disable", "restart", "remove"} {
		action := action
		root.AddCommand(&cobra.Command{Use: action, Short: map[string]string{"enable": "启用服务", "disable": "停用服务", "restart": "重启单个服务并重新读取密钥", "remove": "移除服务配置，保留备份与计量"}[action], RunE: func(cmd *cobra.Command, _ []string) error {
			if id == "" {
				return fmt.Errorf("必须指定 --id")
			}
			c, err := load()
			if err != nil {
				return err
			}
			return localservice.Edit(c, id, func(s *conf.LocalService) error {
				if s.Type == "" {
					return fmt.Errorf("服务未保存到 LocalServiceDir")
				}
				if action == "remove" {
					s.Type = ""
				} else if action == "restart" {
					s.Enabled = true
					s.Revision++
				} else {
					s.Enabled = action == "enable"
				}
				return nil
			})
		}})
	}
	peers := &cobra.Command{Use: "peers", Short: "管理 WireGuard/AmneziaWG Peer"}
	root.AddCommand(peers)
	peerPut := &cobra.Command{Use: "put", Short: "添加或编辑 Peer", RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		var peer conf.WireGuardPeer
		if err = read(file, &peer); err != nil {
			return err
		}
		return localservice.Edit(c, id, func(s *conf.LocalService) error {
			if s.WireGuard == nil {
				return fmt.Errorf("服务不是 WireGuard/AmneziaWG")
			}
			for i, p := range s.WireGuard.Peers {
				if p.UID == peer.UID {
					s.WireGuard.Peers[i] = peer
					return nil
				}
			}
			s.WireGuard.Peers = append(s.WireGuard.Peers, peer)
			return nil
		})
	}}
	peerPut.Flags().StringVarP(&file, "file", "f", "", "Peer JSON 文件")
	_ = peerPut.MarkFlagRequired("file")
	peers.AddCommand(peerPut)
	peerRemove := &cobra.Command{Use: "remove", Short: "撤销 Peer", RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		return localservice.Edit(c, id, func(s *conf.LocalService) error {
			if s.WireGuard == nil {
				return fmt.Errorf("服务不是 WireGuard/AmneziaWG")
			}
			for i, p := range s.WireGuard.Peers {
				if p.UID == uid {
					s.WireGuard.Peers = append(s.WireGuard.Peers[:i], s.WireGuard.Peers[i+1:]...)
					return nil
				}
			}
			return fmt.Errorf("Peer 不存在")
		})
	}}
	peerRemove.Flags().IntVar(&uid, "uid", 0, "Peer UID")
	peers.AddCommand(peerRemove)
	peerList := &cobra.Command{Use: "list", Short: "查看 Peer 分配与计量", RunE: list.RunE}
	peers.AddCommand(peerList)
	export := &cobra.Command{Use: "export", Short: "将客户端配置写入私有文件", RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		specs, err := localservice.LoadSpecs(c)
		if err != nil {
			return err
		}
		if _, err = os.Stat(output); err == nil {
			return fmt.Errorf("导出文件已存在，选择新路径以保留原文件")
		}
		for _, s := range specs {
			if s.ID == id {
				s, _, err = localservice.Allocate(s, filepath.Join(c.LocalServiceDir, ".state", id+".addresses.json"))
				if err != nil {
					return err
				}
				data, err := localservice.Export(s, uid, clientKey)
				if err != nil {
					return err
				}
				return localservice.WritePrivate(output, data)
			}
		}
		return fmt.Errorf("服务不存在")
	}}
	export.Flags().StringVarP(&output, "output", "o", "", "导出路径")
	_ = export.MarkFlagRequired("output")
	export.Flags().StringVar(&clientKey, "client-key-file", "", "客户端已有私钥文件")
	export.Flags().IntVar(&uid, "uid", 0, "Peer UID")
	root.AddCommand(export)
	var keyOutput string
	keygen := &cobra.Command{Use: "keygen", Short: "生成私有 WireGuard 密钥文件并输出公钥", RunE: func(cmd *cobra.Command, _ []string) error {
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(keyOutput, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(base64.StdEncoding.EncodeToString(key.Bytes()) + "\n")
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"public_key": base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())})
	}}
	keygen.Flags().StringVarP(&keyOutput, "output", "o", "", "新私钥文件路径")
	_ = keygen.MarkFlagRequired("output")
	root.AddCommand(keygen)
	command.AddCommand(root)
}
