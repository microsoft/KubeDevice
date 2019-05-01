package kubecri

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Microsoft/KubeDevice/kubecri/pkg/device"
	"github.com/Microsoft/KubeDevice/kubecri/pkg/kubeadvertise"
	"github.com/Microsoft/KubeDevice/kubeinterface"

	"github.com/Microsoft/KubeDevice-API/pkg/types"
	"k8s.io/klog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	kubeletapp "k8s.io/kubernetes/cmd/kubelet/app"
	"k8s.io/kubernetes/cmd/kubelet/app/options"
	kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	runtimeapi "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
	"k8s.io/kubernetes/pkg/kubelet/dockershim"
	dockerremote "k8s.io/kubernetes/pkg/kubelet/dockershim/remote"
	"k8s.io/kubernetes/pkg/kubelet/server/streaming"
	kubelettypes "k8s.io/kubernetes/pkg/kubelet/types"
)

// implementation of runtime service -- have to implement entire docker service
type dockerExtService struct {
	dockershim.DockerService
	kubeclient *clientset.Clientset
	devmgr     *device.DevicesManager
}

func (d *dockerExtService) modifyContainerConfig(pod *types.PodInfo, cont *types.ContainerInfo, config *runtimeapi.ContainerConfig) error {
	// allocate devices for container
	mounts, devices, envs, err := d.devmgr.AllocateDevices(pod, cont)
	if err != nil {
		return err
	}
	klog.V(3).Infof("New devices to add: %v", devices)
	for _, device := range devices {
		config.Devices = append(config.Devices, &runtimeapi.Device{
			HostPath:      device,
			ContainerPath: device,
			Permissions:   "mrw",
		})
	}
	klog.V(3).Infof("New mounts to add: %v", mounts)
	for _, volume := range mounts {
		config.Mounts = append(config.Mounts, &runtimeapi.Mount{
			HostPath:      volume.HostPath,
			ContainerPath: volume.ContainerPath,
			Readonly:      volume.Readonly,
		})
	}
	klog.V(3).Infof("New envs to add: %v", envs)
	for envKey, envVal := range envs {
		config.Envs = append(config.Envs, &runtimeapi.KeyValue{
			Key:   envKey,
			Value: envVal,
		})
	}
	return nil
}

// DockerService => RuntimeService => ContainerManager
func (d *dockerExtService) CreateContainer(ctx context.Context, r *runtimeapi.CreateContainerRequest) (*runtimeapi.CreateContainerResponse, error) {
	// overwrite config.Devices here & then call CreateContainer ...
	config := r.Config
	podName := config.Labels[kubelettypes.KubernetesPodNameLabel]
	podNameSpace := config.Labels[kubelettypes.KubernetesPodNamespaceLabel]
	containerName := config.Labels[kubelettypes.KubernetesContainerNameLabel]
	klog.V(3).Infof("Creating container for pod %v container %v", podName, containerName)
	opts := metav1.GetOptions{}
	pod, err := d.kubeclient.CoreV1().Pods(podNameSpace).Get(podName, opts)
	if err != nil {
		klog.Errorf("Retrieving pod %v gives error %v", podName, err)
	}
	klog.V(3).Infof("Pod Spec: %v", pod.Spec)
	// convert to local podInfo structure using annotations available
	podInfo, err := kubeinterface.KubePodInfoToPodInfo(pod, false)
	if err != nil {
		return nil, err
	}
	// modify the container config
	err = d.modifyContainerConfig(podInfo, podInfo.GetContainerInPod(containerName), config)
	if err != nil {
		return nil, err
	}
	return d.DockerService.CreateContainer(ctx, r)
}

// func (d *dockerExtService) ExecSync(containerID string, cmd []string, timeout time.Duration) (stdout []byte, stderr []byte, err error) {
// 	klog.V(5).Infof("Exec sync called %v Cmd %v", containerID, cmd)
// 	return d.DockerService.ExecSync(containerID, cmd, timeout)
// }

// func (d *dockerExtService) Exec(request *runtimeapi.ExecRequest) (*runtimeapi.ExecResponse, error) {
// 	response, err := d.DockerService.Exec(request)
// 	klog.V(5).Infof("Exec called %v\n Response %v", request, response)
// 	return response, err
// }

// =====================
// Start the shim
func DockerExtInit(f *options.KubeletFlags, c *kubeletconfig.KubeletConfiguration, client *clientset.Clientset, dev *device.DevicesManager) error {
	r := &f.ContainerRuntimeOptions

	// Initialize docker client configuration.
	dockerClientConfig := &dockershim.ClientConfig{
		DockerEndpoint:            r.DockerEndpoint,
		RuntimeRequestTimeout:     c.RuntimeRequestTimeout.Duration,
		ImagePullProgressDeadline: r.ImagePullProgressDeadline.Duration,
	}

	// Initialize network plugin settings.
	pluginSettings := dockershim.NetworkPluginSettings{
		HairpinMode:        kubeletconfig.HairpinMode(c.HairpinMode),
		NonMasqueradeCIDR:  f.NonMasqueradeCIDR,
		PluginName:         r.NetworkPluginName,
		PluginConfDir:      r.CNIConfDir,
		PluginBinDirString: r.CNIBinDir,
		MTU:                int(r.NetworkPluginMTU),
	}

	// Initialize streaming configuration.
	streamingConfig := &streaming.Config{
		StreamIdleTimeout:               c.StreamingConnectionIdleTimeout.Duration,
		StreamCreationTimeout:           streaming.DefaultConfig.StreamCreationTimeout,
		SupportedRemoteCommandProtocols: streaming.DefaultConfig.SupportedRemoteCommandProtocols,
		SupportedPortForwardProtocols:   streaming.DefaultConfig.SupportedPortForwardProtocols,
	}

	// Initialize TLS - only needed for streaming redirect, streaming redirect needs open IP on worker nodes
	tlsOptions, err := kubeletapp.InitializeTLS(f, c)
	if err != nil {
		return err
	}
	if r.RedirectContainerStreaming {
		ipName, nodeName, err := kubeadvertise.GetHostName(f)
		klog.V(2).Infof("Using ipname %v nodeName %v", ipName, nodeName)
		if err != nil {
			return err
		}
		streamingConfig.Addr = fmt.Sprintf("%s:%d", ipName, c.Port)
		streamingConfig.TLSConfig = tlsOptions.Config
	} else {
		streamingConfig.BaseURL = &url.URL{Path: "/cri/"}
	}

	// if !r.RedirectContainerStreaming, then proxy commands to docker service
	//      client->APIServer->kubelet->kubecri_shim->kubecri(dockerservice)
	// client->APIServer->kubelet is already TLS (i.e. secure), but overhead (traversing many components)
	// else if r.ReirectContainerStreaming, then upon connection,
	//      client->APIServer->kublet->kubecri_shim->kubecri(dockerservice) gives redirect
	// client->kubecri(dockerservice) - go directly to streaming server, streaming server should use TLS, then it is secure
	// client->APIServer is with TLS, APIServer->kubelet is TLS, kubelet->kubecri_shim is localhost REST, kubecri_shim->kubecri is linux socket
	ds, err := dockershim.NewDockerService(dockerClientConfig, r.PodSandboxImage, streamingConfig, &pluginSettings,
		f.RuntimeCgroups, c.CgroupDriver, r.DockershimRootDirectory, !r.RedirectContainerStreaming)
	if err != nil {
		return err
	}

	dsExt := &dockerExtService{DockerService: ds, kubeclient: client, devmgr: dev}
	if err := dsExt.Start(); err != nil {
		return err
	}

	klog.V(2).Infof("Starting the GRPC server for the docker CRI shim.")
	server := dockerremote.NewDockerServer(f.RemoteRuntimeEndpoint, dsExt)
	if err := server.Start(); err != nil {
		return err
	}

	// Start the streaming server
	if r.RedirectContainerStreaming {
		s := &http.Server{
			Addr:           net.JoinHostPort(c.Address, strconv.Itoa(int(c.Port))),
			Handler:        dsExt,
			TLSConfig:      tlsOptions.Config,
			MaxHeaderBytes: 1 << 20,
		}
		if tlsOptions != nil {
			// this will listen forever
			return s.ListenAndServeTLS(tlsOptions.CertFile, tlsOptions.KeyFile)
		}
		return s.ListenAndServe()
	}

	var stop = make(chan struct{})
	<-stop // wait forever
	close(stop)
	return nil
}

// func RunDockershim(f *options.KubeletFlags, c *kubeletconfiginternal.KubeletConfiguration, stopCh <-chan struct{}) error {
// 	r := &f.ContainerRuntimeOptions

// 	// Initialize docker client configuration.
// 	dockerClientConfig := &dockershim.ClientConfig{
// 		DockerEndpoint:            r.DockerEndpoint,
// 		RuntimeRequestTimeout:     c.RuntimeRequestTimeout.Duration,
// 		ImagePullProgressDeadline: r.ImagePullProgressDeadline.Duration,
// 	}

// 	// Initialize network plugin settings.
// 	pluginSettings := dockershim.NetworkPluginSettings{
// 		HairpinMode:        kubeletconfiginternal.HairpinMode(c.HairpinMode),
// 		NonMasqueradeCIDR:  f.NonMasqueradeCIDR,
// 		PluginName:         r.NetworkPluginName,
// 		PluginConfDir:      r.CNIConfDir,
// 		PluginBinDirString: r.CNIBinDir,
// 		MTU:                int(r.NetworkPluginMTU),
// 	}

// 	// Initialize streaming configuration. (Not using TLS now)
// 	streamingConfig := &streaming.Config{
// 		// Use a relative redirect (no scheme or host).
// 		BaseURL:                         &url.URL{Path: "/cri/"},
// 		StreamIdleTimeout:               c.StreamingConnectionIdleTimeout.Duration,
// 		StreamCreationTimeout:           streaming.DefaultConfig.StreamCreationTimeout,
// 		SupportedRemoteCommandProtocols: streaming.DefaultConfig.SupportedRemoteCommandProtocols,
// 		SupportedPortForwardProtocols:   streaming.DefaultConfig.SupportedPortForwardProtocols,
// 	}

// 	// Standalone dockershim will always start the local streaming server.
// 	ds, err := dockershim.NewDockerService(dockerClientConfig, r.PodSandboxImage, streamingConfig, &pluginSettings,
// 		f.RuntimeCgroups, c.CgroupDriver, r.DockershimRootDirectory, true /*startLocalStreamingServer*/)
// 	if err != nil {
// 		return err
// 	}
// 	klog.V(2).Infof("Starting the GRPC server for the docker CRI shim.")
// 	server := dockerremote.NewDockerServer(f.RemoteRuntimeEndpoint, ds)
// 	if err := server.Start(); err != nil {
// 		return err
// 	}
// 	<-stopCh
// 	return nil
// }
