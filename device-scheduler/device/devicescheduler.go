package device

import (
	"plugin"

	sctypes "github.com/Microsoft/KubeDevice-API/pkg/devicescheduler"
	"github.com/Microsoft/KubeDevice-API/pkg/types"
	"github.com/Microsoft/KubeDevice-API/pkg/utils"
	"k8s.io/klog"
)

// var DeviceSchedulerRegistry = map[string]reflect.Type{
// 	(&nvidia.NvidiaGPUScheduler{}).GetName(): reflect.TypeOf(nvidia.NvidiaGPUScheduler{}),
// }

type DevicesScheduler struct {
	Devices           []sctypes.DeviceScheduler
	hasGroupScheduler bool
	score             map[string]map[string]float64
	maxScore          map[string]float64
}

// essentially a static variable
var DeviceScheduler = &DevicesScheduler{
	hasGroupScheduler: false,
	score:             make(map[string]map[string]float64),
	maxScore:          make(map[string]float64),
}

func (ds *DevicesScheduler) RemoveAll() {
	ds.hasGroupScheduler = false
	ds.Devices = nil
	ds.score = make(map[string]map[string]float64)
	ds.maxScore = make(map[string]float64)
}

func (ds *DevicesScheduler) AddDevice(device sctypes.DeviceScheduler) {
	usingGroupScheduler := device.UsingGroupScheduler()
	if !ds.hasGroupScheduler {
		ds.Devices = append(ds.Devices, device)
	} else {
		ds.Devices = append(ds.Devices[:len(ds.Devices)-1], device, ds.Devices[len(ds.Devices)-1])
	}
	if usingGroupScheduler && !ds.hasGroupScheduler {
		klog.V(3).Infof("Adding group device for group scheduler")
		ds.Devices = append(ds.Devices, &GrpDevice{})
		ds.hasGroupScheduler = true
	}
	klog.V(3).Infof("Registering device scheduler %s, using group scheduler %v", device, usingGroupScheduler)
}

func (ds *DevicesScheduler) AddDevicesSchedulerFromPlugins(pluginNames []string) {
	for _, pluginName := range pluginNames {
		var device sctypes.DeviceScheduler
		device = nil
		p, err := plugin.Open(pluginName)
		if err == nil {
			f, err := p.Lookup("CreateDeviceSchedulerPlugin")
			if err == nil {
				err, d := f.(func() (error, sctypes.DeviceScheduler))()
				if err == nil {
					device = d
				} else {
					klog.Errorf("Schduler Plugin %s creation fails with error %v", pluginName, err)
				}
			} else {
				klog.Errorf("Scheudler Plugin %s function lookup fails with error %v", pluginName, err)
			}
		} else {
			klog.Errorf("Scheduler plugin %s open fails with error %v", pluginName, err)
		}
		if device == nil {
			klog.Errorf("Unable to add scheduler plugin %s", pluginName)
		} else {
			ds.AddDevice(device)
		}
	}
}

// AddNode adds node reources to devices scheduler
func (ds *DevicesScheduler) AddNode(nodeName string, nodeInfo *types.NodeInfo) {
	//fmt.Printf("AddNodeDevices: %v\n", ds.Devices)
	for _, d := range ds.Devices {
		utils.Logf(3, "Node: %v AddDevice: %v", nodeName, d.GetName())
		d.AddNode(nodeName, nodeInfo)
	}
}

// RemoveNode removes node resources
func (ds *DevicesScheduler) RemoveNode(nodeName string) {
	for _, d := range ds.Devices {
		d.RemoveNode(nodeName)
	}
}

// func (ds *DevicesScheduler) CreateAndAddDeviceScheduler(device string) error {
// 	o := reflect.New(DeviceSchedulerRegistry[device])
// 	t := o.Interface().(types.DeviceScheduler)
// 	ds.AddDevice(t)
// 	return nil
// }

// predicate
func (ds *DevicesScheduler) PodFitsResources(podInfo *types.PodInfo, nodeInfo *types.NodeInfo, fillAllocateFrom bool) (bool, []sctypes.PredicateFailureReason, float64) {
	totalScore := 0.0
	totalFit := true
	var totalReasons []sctypes.PredicateFailureReason
	for i, d := range ds.Devices {
		fit, reasons, score := d.PodFitsDevice(nodeInfo, podInfo, fillAllocateFrom)
		utils.Logf(4, "Device: %v Fit: %v Reasons: %v Score: %v", i, fit, reasons, score)
		//fmt.Printf("I: %v Score: %v\n", i, score)
		// could early terminate - but score will not be correct then
		totalScore += score
		totalFit = totalFit && fit
		totalReasons = append(totalReasons, reasons...)
	}
	utils.AssignMap(ds.score, []string{podInfo.Name, nodeInfo.Name}, 0.0)
	if totalFit {
		ds.score[podInfo.Name][nodeInfo.Name] = totalScore
		if totalScore > ds.maxScore[podInfo.Name] {
			ds.maxScore[podInfo.Name] = totalScore
		}
	} else {
		ds.score[podInfo.Name][nodeInfo.Name] = 0.0
	}
	utils.Logf(4, "Device PodFitsResources on node %v: Fit: %v Reasons: %v Score: %v",
		nodeInfo.Name, totalFit, totalReasons, totalScore)
	return totalFit, totalReasons, totalScore
}

// priority - returns number between 0 and 1 (1 for node with maximum score)
func (ds *DevicesScheduler) PodPriority(podInfo *types.PodInfo, nodeInfo *types.NodeInfo) float64 {
	maxScore, ok := ds.maxScore[podInfo.Name]
	if !ok {
		klog.Errorf("Score not found")
		return 0.0
	}
	score, ok := ds.score[podInfo.Name][nodeInfo.Name]
	if !ok {
		klog.Errorf("Score not found")
		return 0.0
	}
	if maxScore != 0.0 {
		return score / maxScore
	}
	return 0.0
}

func (ds *DevicesScheduler) RemovePodFromScore(podInfo *types.PodInfo) {
	_, ok := ds.score[podInfo.Name]
	if ok {
		delete(ds.score, podInfo.Name)
	}
	_, ok = ds.maxScore[podInfo.Name]
	if ok {
		delete(ds.maxScore, podInfo.Name)
	}
}

// allocate devices & write into annotations
func (ds *DevicesScheduler) PodAllocate(podInfo *types.PodInfo, nodeInfo *types.NodeInfo) error {
	for _, d := range ds.Devices {
		err := d.PodAllocate(nodeInfo, podInfo)
		if err != nil {
			return err
		}
	}
	ds.RemovePodFromScore(podInfo)
	return nil
}

// take pod resources used by devices
func (ds *DevicesScheduler) TakePodResources(podInfo *types.PodInfo, nodeInfo *types.NodeInfo) error {
	for _, d := range ds.Devices {
		err := d.TakePodResources(nodeInfo, podInfo)
		if err != nil {
			return err
		}
	}
	return nil
}

// return pod resources used by devices
func (ds *DevicesScheduler) ReturnPodResources(podInfo *types.PodInfo, nodeInfo *types.NodeInfo) error {
	for _, d := range ds.Devices {
		err := d.ReturnPodResources(nodeInfo, podInfo)
		if err != nil {
			return err
		}
	}
	return nil
}
