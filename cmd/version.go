package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	vCore "github.com/shmily2-1/V2bX-2/core"
	"github.com/spf13/cobra"
)

var (
	version  = "TempVersion" //use ldflags replace
	codename = "V2bX-2"
	intro    = "A V2board backend based on multi core"
)

var versionCommand = cobra.Command{
	Use:   "version",
	Short: "Print version info",
	Run: func(_ *cobra.Command, _ []string) {
		showVersion()
	},
}

func init() {
	command.AddCommand(&versionCommand)
}

func showVersion() {
	fmt.Println(` 
  _/      _/    _/_/    _/        _/      _/   
 _/      _/  _/    _/  _/_/_/      _/  _/      
_/      _/      _/    _/    _/      _/         
 _/  _/      _/      _/    _/    _/  _/        
  _/      _/_/_/_/  _/_/_/    _/      _/        
                                                `)
	fmt.Printf("%s %s (%s) \n", codename, version, intro)
	cores := vCore.RegisteredCore()
	sort.Strings(cores)
	fmt.Printf("Go: %s; cores: %s\n", runtime.Version(), strings.Join(cores, ", "))
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			switch dep.Path {
			case "github.com/xtls/xray-core", "github.com/sagernet/sing-box", "github.com/apernet/hysteria/core/v2":
				suffix := ""
				if dep.Replace != nil {
					suffix = " + V2bX-2 compatibility patches"
				}
				fmt.Printf("%s %s%s\n", dep.Path, dep.Version, suffix)
			}
		}
	}
	//fmt.Printf("Supported cores: %s\n", strings.Join(vCore.RegisteredCore(), ", "))
	// Warning
	//fmt.Println(Warn("This version need V2board version >= 1.7.0."))
	//fmt.Println(Warn("The version have many changed for config, please check your config file"))
}
