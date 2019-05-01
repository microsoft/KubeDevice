package app

import (
	"fmt"
	"io/ioutil"
	"path"

	"k8s.io/klog"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	utilfeature "k8s.io/apiserver/pkg/util/feature"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/kubernetes/cmd/kubelet/app/options"
	kubeletconfigvalidation "k8s.io/kubernetes/pkg/kubelet/apis/config/validation"
	utilflag "k8s.io/kubernetes/pkg/util/flag"
	"k8s.io/kubernetes/pkg/version/verflag"

	"github.com/Microsoft/KubeDevice/kubecri/pkg/device"
	"github.com/Microsoft/KubeDevice/kubecri/pkg/kubeadvertise"
	"github.com/Microsoft/KubeDevice/kubecri/pkg/kubecri"
)

// ====================
// Main
type kubecriConfig struct {
	DevicePath string
}

func (cfg *kubecriConfig) New() {
	cfg.DevicePath = "/usr/local/KubeExt/devices"
}

func Run(kubecriCfg *kubecriConfig, kubeletServer *options.KubeletServer) {
	// add device plugins and start device manager
	var devicePlugins []string
	devicePluginFiles, err := ioutil.ReadDir(kubecriCfg.DevicePath)
	if err != nil {
		klog.Errorf("Unable to list devices, skipping adding of devices - error %v", err)
	}
	for _, f := range devicePluginFiles {
		devicePlugins = append(devicePlugins, path.Join(kubecriCfg.DevicePath, f.Name()))
	}
	device.DeviceManager.AddDevicesFromPlugins(devicePlugins)
	device.DeviceManager.Start()

	done := make(chan bool)
	// start the device advertiser
	da, err := kubeadvertise.StartDeviceAdvertiser(kubeletServer, done)
	if err != nil {
		klog.Fatal(err)
	}
	// run the deviceshim
	if err := kubecri.DockerExtInit(&kubeletServer.KubeletFlags, &kubeletServer.KubeletConfiguration, da.KubeClient, da.DevMgr); err != nil {
		klog.Fatal(err)
	}
	<-done // wait forever
	done <- true
}

// NewCRICommand creates a *cobra.Command object with default parameters
func NewCRICommand() *cobra.Command {
	cleanFlagSet := pflag.NewFlagSet("CRI Shim", pflag.ContinueOnError)
	cleanFlagSet.SetNormalizeFunc(cliflag.WordSepNormalizeFunc)
	kubeletFlags := options.NewKubeletFlags()
	kubeletConfig, err := options.NewKubeletConfiguration()
	kubecriCfg := kubecriConfig{}
	kubecriCfg.New()
	// programmer error
	if err != nil {
		klog.Fatal(err)
	}

	// cmd.Run is a closure
	cmd := &cobra.Command{
		Use:  "CRI Shim",
		Long: `The CRI Shim`,
		// The Kubelet has special flag parsing requirements to enforce flag precedence rules,
		// so we do all our parsing manually in Run, below.
		// DisableFlagParsing=true provides the full set of flags passed to the kubelet in the
		// `args` arg to Run, without Cobra's interference.
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			// initial flag parse, since we disable cobra's flag parsing
			if err := cleanFlagSet.Parse(args); err != nil {
				cmd.Usage()
				klog.Fatal(err)
			}

			// check if there are non-flag arguments in the command line
			cmds := cleanFlagSet.Args()
			if len(cmds) > 0 {
				cmd.Usage()
				klog.Fatalf("unknown command: %s", cmds[0])
			}

			// short-circuit on help
			help, err := cleanFlagSet.GetBool("help")
			if err != nil {
				klog.Fatal(`"help" flag is non-bool, programmer error, please correct`)
			}
			if help {
				cmd.Help()
				return
			}

			// short-circuit on verflag
			verflag.PrintAndExitIfRequested()
			utilflag.PrintFlags(cleanFlagSet)

			// set feature gates from initial flags-based config
			if err := utilfeature.DefaultMutableFeatureGate.SetFromMap(kubeletConfig.FeatureGates); err != nil {
				klog.Fatal(err)
			}

			// validate the initial KubeletFlags
			if err := options.ValidateKubeletFlags(kubeletFlags); err != nil {
				klog.Fatal(err)
			}

			// load kubelet config file, if provided
			if configFile := kubeletFlags.KubeletConfigFile; len(configFile) > 0 {
				klog.Fatal(fmt.Errorf("Not supported - configuration file"))
			}

			// We always validate the local configuration (command line + config file).
			// This is the default "last-known-good" config for dynamic config, and must always remain valid.
			if err := kubeletconfigvalidation.ValidateKubeletConfiguration(kubeletConfig); err != nil {
				klog.Fatal(err)
			}

			// construct a KubeletServer from kubeletFlags and kubeletConfig
			kubeletServer := &options.KubeletServer{
				KubeletFlags:         *kubeletFlags,
				KubeletConfiguration: *kubeletConfig,
			}

			// run the shim
			klog.V(5).Infof("KubeletConfiguration: %#v", kubeletServer.KubeletConfiguration)
			Run(&kubecriCfg, kubeletServer)
		},
	}

	// keep cleanFlagSet separate, so Cobra doesn't pollute it with the global flags
	kubeletFlags.AddFlags(cleanFlagSet)
	options.AddKubeletConfigFlags(cleanFlagSet, kubeletConfig)
	options.AddGlobalFlags(cleanFlagSet)
	cleanFlagSet.BoolP("help", "h", false, fmt.Sprintf("help for %s", cmd.Name()))

	// Add other options needed for cri shim
	cleanFlagSet.StringVar(&kubecriCfg.DevicePath, "cridevices", kubecriCfg.DevicePath, "The path where device plugins are located")

	// ugly, but necessary, because Cobra's default UsageFunc and HelpFunc pollute the flagset with global flags
	const usageFmt = "Usage:\n  %s\n\nFlags:\n%s"
	cmd.SetUsageFunc(func(cmd *cobra.Command) error {
		fmt.Fprintf(cmd.OutOrStderr(), usageFmt, cmd.UseLine(), cleanFlagSet.FlagUsagesWrapped(2))
		return nil
	})
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n"+usageFmt, cmd.Long, cmd.UseLine(), cleanFlagSet.FlagUsagesWrapped(2))
	})

	return cmd
}
