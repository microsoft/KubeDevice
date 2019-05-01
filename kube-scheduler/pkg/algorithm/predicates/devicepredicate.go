package predicates

import (
	"github.com/Microsoft/KubeDevice/device-scheduler/device"
	"github.com/Microsoft/KubeDevice/kube-scheduler/pkg/nodeinfo"
	"k8s.io/api/core/v1"
	"k8s.io/klog"
)

func PodFitsDevices(pod *v1.Pod, meta PredicateMetadata, node *nodeinfo.NodeInfo) (bool, []PredicateFailureReason, error) {
	klog.V(4).Infof("Running PodFitsDevice on %v on node %v", pod.ObjectMeta.Name, node.Node().ObjectMeta.Name)
	podInfo, nodeInfo, err := nodeinfo.GetPodAndNode(pod, node, true)
	if err != nil {
		klog.Errorf("GetPodAndNode encounters error %v", err)
		return false, nil, err
	}
	klog.V(4).Infof("Attempting to schedule devices for pod %+v on node %+v", podInfo, nodeInfo)
	fits, reasons, _ := device.DeviceScheduler.PodFitsResources(podInfo, nodeInfo, false) // no need to fill allocatefrom yet
	var failureReasons []PredicateFailureReason
	for _, reason := range reasons {
		rName, requested, used, capacity := reason.GetInfo()
		krName := string(rName)
		failureReasons = append(failureReasons, NewInsufficientResourceError(v1.ResourceName(krName), requested, used, capacity))
	}
	return fits, failureReasons, nil
}
