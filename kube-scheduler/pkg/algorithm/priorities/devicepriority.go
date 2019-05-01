package priorities

import (
	"github.com/Microsoft/KubeDevice/device-scheduler/device"
	schedulerapi "github.com/Microsoft/KubeDevice/kube-scheduler/pkg/api"
	"github.com/Microsoft/KubeDevice/kube-scheduler/pkg/nodeinfo"
	"k8s.io/klog"
	"k8s.io/api/core/v1"
)

// prioritizer
func PodDevicePriority(pod *v1.Pod, meta interface{}, node *nodeinfo.NodeInfo) (schedulerapi.HostPriority, error) {
	podInfo, nodeInfo, err := nodeinfo.GetPodAndNode(pod, node, true)
	if err != nil {
		klog.Errorf("GetPodAndNode encounters error %v", err)
		return schedulerapi.HostPriority{}, err
	}
	score := int(float64(schedulerapi.MaxPriority) * device.DeviceScheduler.PodPriority(podInfo, nodeInfo))
	klog.V(4).Infof("Device priority for pod %+v on node %+v is %d", podInfo, nodeInfo, score)
	return schedulerapi.HostPriority{
		Host:  node.Node().Name,
		Score: score,
	}, nil
}
