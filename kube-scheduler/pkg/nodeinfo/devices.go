package nodeinfo

import (
	"fmt"

	extypes "github.com/Microsoft/KubeDevice-API/pkg/types"
	"github.com/Microsoft/KubeDevice/device-scheduler/device"
	"github.com/Microsoft/KubeDevice/kubeinterface"
	"k8s.io/klog"

	"k8s.io/api/core/v1"
)

func GetPodAndNode(pod *v1.Pod, node *NodeInfo, invalidatePodAnnotations bool) (*extypes.PodInfo, *extypes.NodeInfo, error) {
	// grab node information
	nodeInfo := node.nodeEx
	// if nodeInfo == nil {
	// 	if node.Node() != nil {
	// 		//return nil, nil, fmt.Errorf("node not found")
	// 		nodeInfoGet, err := kubeinterface.AnnotationToNodeInfo(&node.Node().ObjectMeta)
	// 		klog.V(2).Infof("Node Info not present yet, use annotations to recompute")
	// 		if err != nil {
	// 			return nil, nil, err
	// 		}
	// 		nodeInfo = nodeInfoGet
	// 	} else {
	// 		nodeInfo = extypes.NewNodeInfo()
	// 		klog.V(2).Infof("Node Info not present yet, set to new struct")
	// 	}
	// }
	podInfo, err := kubeinterface.KubePodInfoToPodInfo(pod, invalidatePodAnnotations)
	if err != nil {
		return nil, nil, err
	}
	if !invalidatePodAnnotations && node.node != nil {
		nodeName := node.node.ObjectMeta.Name
		// empty string for default pods (not from scheduler)
		if (podInfo.NodeName != nodeName) && (podInfo.NodeName != "") {
			errStr := fmt.Sprintf("Node name is not correct - pod expects %v, but node has %v", podInfo.NodeName, nodeName)
			klog.Errorf(errStr)
			return nil, nil, fmt.Errorf(errStr)
		}
	}
	return podInfo, nodeInfo, nil
}

func TakePodDeviceResources(pod *v1.Pod, node *NodeInfo) error {
	// convert pod annotations to resources and use them -- should not return error as pod annotations should be correct
	podInfo, nodeInfo, err := GetPodAndNode(pod, node, false)
	if err != nil {
		return err
	}
	return device.DeviceScheduler.TakePodResources(podInfo, nodeInfo)
}

func ReturnPodDeviceResources(pod *v1.Pod, node *NodeInfo) error {
	podInfo, nodeInfo, err := GetPodAndNode(pod, node, false)
	if err != nil {
		return err
	}
	return device.DeviceScheduler.ReturnPodResources(podInfo, nodeInfo)
}
