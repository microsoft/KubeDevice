package device

import (
	"plugin"
	"reflect"

	devtypes "github.com/Microsoft/KubeDevice-API/pkg/device"
	"github.com/Microsoft/KubeDevice-API/pkg/types"
	"k8s.io/klog"
)

// DeviceManager manages multiple devices
type DevicesManager struct {
	Operational []bool
	Devices     []devtypes.Device
}

// essentially a static variable
var DeviceManager = &DevicesManager{}

// AddDevice adds a device to the manager
func (d *DevicesManager) AddDevice(device devtypes.Device) {
	d.Devices = append(d.Devices, device)
	d.Operational = append(d.Operational, false) // set to true once start is successful
}

func (d *DevicesManager) NewAndAddDevice(t devtypes.Device) error {
	err := t.New()
	if err != nil {
		return err
	}
	d.AddDevice(t)
	return nil
}

func (d *DevicesManager) CreateAndAddDeviceType(devType reflect.Type) error {
	o := reflect.New(devType)
	t := o.Interface().(devtypes.Device)
	return d.NewAndAddDevice(t)
}

// func (d *DevicesManager) CreateAndAddDevice(deviceName string) error {
// 	return d.CreateAndAddDeviceType(DeviceRegistry[device])
// }

func (d *DevicesManager) AddDevicesFromPlugins(pluginNames []string) {
	for _, pluginName := range pluginNames {
		var device devtypes.Device
		device = nil
		p, err := plugin.Open(pluginName)
		if err == nil {
			f, err := p.Lookup("CreateDevicePlugin")
			if err == nil {
				d, err := f.(func() (devtypes.Device, error))()
				if err == nil {
					device = d
					err = device.New()
					if err != nil {
						klog.Errorf("Device plugin %s New fails with error %v", pluginName, err)
						device = nil
					}
				} else {
					klog.Errorf("Device plugin %s creation with error %v", pluginName, err)
				}
			} else {
				klog.Errorf("Device plugin %s function lookup fails with error %v", pluginName, err)
			}
		} else {
			klog.Errorf("Device plugin %s open fails with error %v", pluginName, err)
		}
		if device == nil {
			klog.Errorf("Unable to add plugin %s", pluginName)
		} else {
			d.AddDevice(device)
		}
	}
}

// Start starts all devices in manager
func (d *DevicesManager) Start() {
	for i, device := range d.Devices {
		err := device.Start()
		if err == nil {
			d.Operational[i] = true
		} else {
			d.Operational[i] = false
		}
	}
}

// UpdateNodeInfo updates a node info strucutre with resources available on device
func (d *DevicesManager) UpdateNodeInfo(info *types.NodeInfo) {
	for i, device := range d.Devices {
		if d.Operational[i] {
			err := device.UpdateNodeInfo(info)
			if err != nil {
				klog.Errorf("Unable to update device %s encounter error %v", device.GetName(), err)
			}
		}
	}
}

// AllocateDevices allocates devices using device manager interface
func (d *DevicesManager) AllocateDevices(pod *types.PodInfo, cont *types.ContainerInfo) ([]devtypes.Mount, []string, map[string]string, error) {
	mounts := []devtypes.Mount{}
	devices := []string{}
	env := make(map[string]string)
	var errRet error
	errRet = nil
	for i, device := range d.Devices {
		if d.Operational[i] {
			mountD, deviceD, envD, err := device.Allocate(pod, cont)
			if err == nil {
				// appending nil to nil is okay
				mounts = append(mounts, mountD...)
				devices = append(devices, deviceD...)
				for k, v := range envD {
					env[k] = v
				}
			} else {
				errRet = err
			}
		}
	}
	return mounts, devices, env, errRet
}
