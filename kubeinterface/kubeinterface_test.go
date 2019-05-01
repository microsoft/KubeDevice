package kubeinterface

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Microsoft/KubeDevice-API/pkg/types"
	"github.com/Microsoft/KubeDevice-API/pkg/utils"
	kubev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func rq(i int64) resource.Quantity {
	return *resource.NewQuantity(i, resource.DecimalSI)
}

func TestConvert(t *testing.T) {
	// test node conversion
	nodeMeta := &metav1.ObjectMeta{Annotations: map[string]string{"OtherAnnotation": "OtherAnnotationValue"}}
	nodeInfo := &types.NodeInfo{
		Name:        "Node0",
		Capacity:    types.ResourceList{"A": 245, "B": 300},
		Allocatable: types.ResourceList{"A": 200, "B": 100},
		Used:        types.ResourceList{"A": 0, "B": 0},
		Scorer:      types.ResourceScorer{"A": 4}, // no scorer for resource "B" is provided
		KubeCap:     make(types.ResourceList),
		KubeAlloc:   make(types.ResourceList),
	}
	NodeInfoToAnnotation(nodeMeta, nodeInfo)
	jsonNode, _ := json.Marshal(nodeInfo)
	annotationExpect := map[string]string{
		"OtherAnnotation":              "OtherAnnotationValue",
		"KubeDevice/DeviceInfo": string(jsonNode),
		// "NodeInfo/Name": "Node0",
		// "NodeInfo/Capacity/A": "245",
		// "NodeInfo/Capacity/B": "300",
		// "NodeInfo/Allocatable/A": "200",
		// "NodeInfo/Allocatable/B": "100",
		// "NodeInfo/Used/A": "0",
		// "NodeInfo/Used/B": "0",
		// "NodeInfo/Scorer/A": "4",
	}
	if !reflect.DeepEqual(annotationExpect, nodeMeta.Annotations) {
		t.Errorf("Node info annotations not what is expected, expected: %+v, have: %+v", annotationExpect, nodeMeta.Annotations)
	}
	nodeInfoGet, err := AnnotationToNodeInfo(nodeMeta, nil)
	if err != nil {
		t.Errorf("Error encountered when converting annotation to node info: %v", err)
	}
	if !utils.CompareNode(nodeInfo, nodeInfoGet) {
		t.Errorf("Get node is not same, expect:\n%+v\n, get:\n%+v\n", nodeInfo, nodeInfoGet)
	}

	// test pod conversion
	init0 := types.ContainerInfo{
		Requests: types.ResourceList{"resource/group/gpu/0/cards": 1, "resource/group/gpu/0/memory": 100000},
	}
	run0 := types.ContainerInfo{
		Requests:     types.ResourceList{"resource/group/gpu/A/cards": 4},
		AllocateFrom: types.ResourceLocation{"resource/group/gpu/0/cards": "CARD1"},
		DevRequests:  types.ResourceList{"resource/group/gpugrp1/A/gpu/0/cards": 90},
	}
	run1 := types.ContainerInfo{
		Requests: types.ResourceList{"resource/group/gpu/A/cards": 6},
		Scorer:   types.ResourceScorer{"resource/group/gpu/A/cards": 10},
	}
	pod0 := types.PodInfo{
		NodeName:          "NodeB",
		InitContainers:    map[string]types.ContainerInfo{"Init0": init0},
		RunningContainers: map[string]types.ContainerInfo{"Run0": run0, "Run1": run1},
	}
	jsonStr, _ := json.Marshal(pod0)
	kubePod := &kubev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "Pod0",
			Annotations: map[string]string{
				"ABCD": "EFGH",
				"KubeDevice/DeviceInfo": string(jsonStr),
				// "PodInfo/InitContainer/Init0/Requests/resource/group/gpu/0/cards": "1",
				// "PodInfo/InitContainer/Init0/Requests/resource/group/gpu/0/memory": "100000",
				// "PodInfo/RunningContainer/Run0/Requests/resource/group/gpu/A/cards": "4",
				// "PodInfo/RunningContainer/Run0/AllocateFrom/resource/group/gpu/0/cards": "CARD1",
				// "PodInfo/RunningContainer/Run0/DevRequests/resource/group/gpugrp1/A/gpu/0/cards": "90",
				// "PodInfo/RunningContainer/Run1/Requests/resource/group/gpu/A/cards": "6",
				// "PodInfo/RunningContainer/Run1/Scorer/resource/group/gpu/A/cards": "10",
				// "PodInfo/ValidForNode": "NodeB",
			},
		},
		Spec: kubev1.PodSpec{
			InitContainers: []kubev1.Container{
				{
					Name:  "Init0",
					Image: "BCDE",
					Resources: kubev1.ResourceRequirements{
						Requests: kubev1.ResourceList{"CPU": rq(4), "Memory": rq(100000), "Other": rq(20)},
						Limits:   kubev1.ResourceList{"CPU": rq(10)},
					},
				},
			},
			Containers: []kubev1.Container{
				{
					Name:  "Run0",
					Image: "RunBCDE",
					Resources: kubev1.ResourceRequirements{
						Requests: kubev1.ResourceList{"CPU": rq(8), "Memory": rq(200000)},
					},
				},
				{
					Name:  "Run1",
					Image: "RunBCDE",
					Resources: kubev1.ResourceRequirements{
						Requests: kubev1.ResourceList{"CPU": rq(4), "Memory": rq(300000), "alpha.kubernetes.io/nvidia-gpu": rq(2)},
					},
				},
			},
		},
	}

	// convert to pod info and clear some annotations
	podInfo, err := KubePodInfoToPodInfo(kubePod, true)
	if err != nil {
		t.Errorf("encounter error %v", err)
	}
	expectedPodInfo := &types.PodInfo{
		Name:     "Pod0",
		NodeName: "",
		Requests: make(types.ResourceList),
		InitContainers: map[string]types.ContainerInfo{
			"Init0": {
				KubeRequests: types.ResourceList{"CPU": 4, "Memory": 100000, "Other": 20},
				Requests:     types.ResourceList{"resource/group/gpu/0/cards": 1, "resource/group/gpu/0/memory": 100000},
				DevRequests:  types.ResourceList{"resource/group/gpu/0/cards": 1, "resource/group/gpu/0/memory": 100000},
				AllocateFrom: types.ResourceLocation{},
				Scorer:       types.ResourceScorer{},
			},
		},
		RunningContainers: map[string]types.ContainerInfo{
			"Run0": {
				KubeRequests: types.ResourceList{"CPU": 8, "Memory": 200000},
				Requests:     types.ResourceList{"resource/group/gpu/A/cards": 4},
				DevRequests:  types.ResourceList{"resource/group/gpu/A/cards": 4},
				AllocateFrom: types.ResourceLocation{},
				Scorer:       types.ResourceScorer{},
			},
			"Run1": {
				KubeRequests: types.ResourceList{"CPU": 4, "Memory": 300000, "alpha.kubernetes.io/nvidia-gpu": 2},
				Requests:     types.ResourceList{"resource/group/gpu/A/cards": 6},
				DevRequests:  types.ResourceList{"resource/group/gpu/A/cards": 6},
				AllocateFrom: types.ResourceLocation{},
				Scorer:       types.ResourceScorer{"resource/group/gpu/A/cards": 10},
			},
		},
	}
	if !utils.ComparePod(podInfo, expectedPodInfo) {
		//comparePod(podInfo, expectedPodInfo)
		t.Errorf("PodInfo is not what is expected\n expect:\n%+v\n have:\n%+v", expectedPodInfo, podInfo)
	}

	// set allocate from and devrequests after translation and allocation
	contCopy := podInfo.InitContainers["Init0"]
	contCopy.DevRequests = types.ResourceList{"resource/group/gpugrp/0/gpu/0/cards": 1, "resource/group/gpugrp/0/gpu/0/memory": 200000}
	contCopy.AllocateFrom = types.ResourceLocation{
		"resource/group/gpugrp/0/gpu/0/cards":  "resource/group/gpugrp/A/gpu/12/cards",
		"resource/group/gpugrp/0/gpu/0/memory": "resource/group/gpugrp/A/gpu/12/memory",
	}
	podInfo.InitContainers["Init0"] = contCopy

	contCopy = podInfo.RunningContainers["Run0"]
	contCopy.DevRequests = types.ResourceList{"resource/group/gpugrp/A/gpu/0/cards": 4}
	contCopy.AllocateFrom = types.ResourceLocation{
		"resource/group/gpugrp/A/gpu/0/cards": "resource/group/gpugrp/0/gpu/43-21/cards",
	}
	podInfo.RunningContainers["Run0"] = contCopy

	contCopy = podInfo.RunningContainers["Run1"]
	contCopy.DevRequests = types.ResourceList{}
	podInfo.RunningContainers["Run1"] = contCopy

	podInfo.NodeName = "NodeNewD"

	// clear existing annotations
	//ClearPodInfoAnnotations(&kubePod.ObjectMeta)
	// convert to annotations
	PodInfoToAnnotation(&kubePod.ObjectMeta, podInfo)

	jsonStr, _ = json.Marshal(podInfo)
	expectedAnnotations := map[string]string{
		"ABCD": "EFGH", // existing
		"KubeDevice/DeviceInfo": string(jsonStr),
		// "PodInfo/InitContainer/Init0/Requests/resource/group/gpu/0/cards": "1",
		// "PodInfo/InitContainer/Init0/Requests/resource/group/gpu/0/memory": "100000",
		// "PodInfo/RunningContainer/Run0/Requests/resource/group/gpu/A/cards": "4",
		// "PodInfo/RunningContainer/Run1/Requests/resource/group/gpu/A/cards": "6",
		// "PodInfo/RunningContainer/Run1/Scorer/resource/group/gpu/A/cards": "10",
		// "PodInfo/RunningContainer/Run0/DevRequests/resource/group/gpugrp/A/gpu/0/cards": "4",
		// "PodInfo/RunningContainer/Run0/AllocateFrom/resource/group/gpugrp/A/gpu/0/cards": "resource/group/gpugrp/0/gpu/43-21/cards",
		// "PodInfo/InitContainer/Init0/DevRequests/resource/group/gpugrp/0/gpu/0/cards": "1",
		// "PodInfo/InitContainer/Init0/DevRequests/resource/group/gpugrp/0/gpu/0/memory": "200000",
		// "PodInfo/InitContainer/Init0/AllocateFrom/resource/group/gpugrp/0/gpu/0/cards": "resource/group/gpugrp/A/gpu/12/cards",
		// "PodInfo/InitContainer/Init0/AllocateFrom/resource/group/gpugrp/0/gpu/0/memory": "resource/group/gpugrp/A/gpu/12/memory",
		// "PodInfo/ValidForNode": "NodeNewD",
	}
	if !reflect.DeepEqual(kubePod.ObjectMeta.Annotations, expectedAnnotations) {
		t.Errorf("Pod annotations are not what is expected\nexpect:\n%v\nhave:\n%v", expectedAnnotations, kubePod.ObjectMeta.Annotations)
		utils.CompareMapStringString(expectedAnnotations, kubePod.ObjectMeta.Annotations)
	}

	// convert back and check podinfo
	podInfo2, err := KubePodInfoToPodInfo(kubePod, false)
	if err != nil {
		t.Errorf("encounter error %v", err)
	}
	if !reflect.DeepEqual(podInfo, podInfo2) {
		t.Errorf("Get back Pod info is not correct\nexpect:\n%v\nhave:\n%v", podInfo, podInfo2)
		utils.ComparePod(podInfo, podInfo2)
	}
}
