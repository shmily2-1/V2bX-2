package cmd

import (
	"fmt"
	"os"

	"github.com/shmily2-1/V2bX-2/api/panel"
	"github.com/shmily2-1/V2bX-2/clientconfig"
	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/localservice"
	"github.com/spf13/cobra"
)

func init() {
	var file, output, server string
	var index, uid int
	var storage bool
	cmd := &cobra.Command{Use: "export-node", Short: "读取面板并导出扩展协议客户端配置", RunE: func(cmd *cobra.Command, _ []string) error {
		c := conf.New()
		if err := c.LoadFromPath(file); err != nil {
			return err
		}
		if index < 0 || index >= len(c.NodeConfig) {
			return fmt.Errorf("node-index is out of range")
		}
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("导出文件已存在，选择新路径")
		}
		n := c.NodeConfig[index]
		api, err := panel.New(&n.ApiConfig)
		if err != nil {
			return err
		}
		info, err := api.GetNodeInfo()
		if err != nil {
			return err
		}
		users, err := api.GetUserList()
		if err != nil {
			return err
		}
		for _, u := range users {
			if u.Id == uid {
				data, err := clientconfig.Build(info, &n.Options, u, server, storage)
				if err != nil {
					return err
				}
				return localservice.WritePrivate(output, append(data, '\n'))
			}
		}
		return fmt.Errorf("authorized UID not found")
	}}
	cmd.Flags().StringVarP(&file, "config", "c", "/etc/V2bX/config.json", "主配置路径")
	cmd.Flags().IntVar(&index, "node-index", 0, "Nodes 数组下标，从 0 开始")
	cmd.Flags().IntVar(&uid, "uid", 0, "授权用户 UID")
	cmd.Flags().StringVar(&server, "server", "", "节点公网域名或 IP")
	cmd.Flags().StringVarP(&output, "output", "o", "", "导出到私有文件")
	cmd.Flags().BoolVar(&storage, "allow-storage-credentials", false, "允许导出隔离的远端存储凭据")
	_ = cmd.MarkFlagRequired("output")
	_ = cmd.MarkFlagRequired("uid")
	command.AddCommand(cmd)
}
