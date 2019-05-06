
# KubeDevice

KubeDevice is an extended plugin framework for Kubernetes and offers more flexibility
than using the default device plugins and scheduler extender.
KubeDevice does not fork the upstream Kubernetes core, but does fork off the Kubernetes scheduler
codebase to build a custom scheduler.

In particular, KubeDevice still allows for 
the allocation of devices to return a list of devices, volume mounts, and environment
variables as device plugins do, but in addition gives the device the flexibility to examine the entire pod specification.
This allows a custom scheduler to write in specific allocation instructions into the pod annotation which can be
examined by the device plugin.
For example, the list of exact devices to use can be written by the scheduler into the pod annotations.

# Architecture

KubeDevice consists of the following three components.
1. Device Advertiser
2. Custom Container Runtime Interface (CRI) - implemented as a shim to the dockershim in Kubernetes core
3. Custom Device Scheduler

Items 1 and 2 are built into a single binary and will run on each agent node where kubelet is running.
Item 3 will run as a drop-in replacement for the Kubernetes scheduler and is built as a binary.

The device advertiser runs as a loop which will periodically advertise devices to the API server.

The custom CRI is used by kubelet as a remote container runtime which overrides the CreateContainer call.
The CreateContainer call is intercepted and the runtimeapi.ContainerConfig is modified depending on 
what the devices want.

The custom scheduler looks at device predicates and device priority when deciding which node to schedule a pod on.

# Usage

To build KubeDevice from source with a `go` installation, simply run the following
```
go get github.com/Microsoft/KubeDevice-API
go get github.com/Microsoft/KubeDevice
cd $GOPATH/src/github.com/Microsoft/KubeDevice
make
```
The following binaries will be available in the `$GOPATH/src/github.com/Microsoft/KubeDevice/_output` directory.
```
kube-scheduler
kubecri
```

Alternatively, KubeDevice binaries can be downloaded as a docker image using `docker pull sanjeevm0:customkube:v1.14.1`.
The same binaries as above will be located in the `/` directory of the container image.

Use `kube-scheduler` exactly with the same arguments as used with the default scheduler. One additional argument is provided
to insert your own plugins which are used in the scheduling decision.
```
--devschedpath string      The path where device scheduler plugins are located (default "/schedulerplugins")
```

On the agent nodes, run `kubelet` as you would normally do, with the following modification to the arguments.
```
--container-runtime=remote
```
Similarly, on the agent nodes, run the `kubecri` binary as a service (or in the same method you use to run the `kubelet` binary).
You can use the identical argument list you use for the `kubelet` binary, with the following additional argument avaiable.
```
--cridevices string  The path where device plugins are located (default "/usr/local/KubeExt/devices")
```

The only additional piece is to copy the `kube-scheduler` and `kubecri` plugin files to the directories specified by the `--devschedpath`
and `--cridevices` respectively, which implement the device specific logic.  The following section will explain what is a plugin
and how to write it.
An example of a scheduler plugin and CRI plugin are available at [https://github.com/Microsoft/KubeGPU].

# Writing your own plugins

For a definition of the types used in the interfaces, take a look at
[https://github.com/Microsoft/KubeDevice-API/blob/master/pkg/types/types.go]

The `NodeInfo` structure specifies information for a given node.
```
// NodeInfo only holds resources being advertised by the device advertisers through annotations
type NodeInfo struct {
	Name        string         `json:"name,omitempty"`
	Capacity    ResourceList   `json:"capacity,omitempty"`
	Allocatable ResourceList   `json:"allocatable,omitempty"` // capacity minus reserverd
	Used        ResourceList   `json:"used,omitempty"`        // being used by pods, must be less than allocatable
	Scorer      ResourceScorer `json:"scorer,omitempty"`
	KubeCap     ResourceList   `json:"-"` // capacity patched into extended resources directly -- stuff default scheduler takes care of
	KubeAlloc   ResourceList   `json:"-"` // stuff default scheduler takes care of
}
```
The maps `KubeCap` and `KubeAlloc` are used to hold the
`Capacity` and `Allocatable` fields from the standard Kubernetes, i.e. `v1.Node.Status.Capacity` and `v1.Node.Status.Allocatable`.

On the scheduler side, `KubeCap` and `KubeAlloc` are populated prior to a `NodeInfo` being given to the plugins and
can be used by the plugins in making scheduling decisions.

On the device advertiser side, any information that the plugin writes into `KubeCap` and `KubeAlloc` will be
advertised as an extended resource which can be used by the default scheduler if desired.
Any information which is meant to be bypassed by the default scheduler entirely should be written into
the `Capacity` and `Allocatable` fields instead.

The `Used` field is used to denote and how much of each resource is being used, and is primarily used by the scheduler.
The `Scorer` can be specified for a node, but any `Scorer` specified by the container information will override it.
The `Scorer` can be used to specify which scoring function to use when determining the node or allocation with the best score.

The pod information is stored in the following.
```
type ContainerInfo struct {
	KubeRequests ResourceList     `json:"-"`                      // requests being handled by kubernetes core - only needed here for resource translation
	Requests     ResourceList     `json:"requests,omitempty"`     // requests specified in annotations in the pod spec
	DevRequests  ResourceList     `json:"devrequests,omitempty"`  // requests after translation - these are used by scheduler to schedule
	AllocateFrom ResourceLocation `json:"allocatefrom,omitempty"` // only valid for extended resources being advertised here
	Scorer       ResourceScorer   `json:"scorer,omitempty"`       // scorer function specified in pod specificiation annotations
}
type PodInfo struct {
	Name              string                   `json:"podname,omitempty"`
	NodeName          string                   `json:"nodename,omitempty"` // the node for which DevRequests and AllocateFrom on ContainerInfo are valid, the node for which PodInfo has been customized
	Requests          ResourceList             `json:"requests,omitempty"` // pod level requests
	InitContainers    map[string]ContainerInfo `json:"initcontainer,omitempty"`
	RunningContainers map[string]ContainerInfo `json:"runningcontainer,omitempty"`
}
```
The `KubeRequests` are the request being made by standard Kubernetes and correspond to `v1.Container.Resources.Requests`.
All other requests should be written into the `Requests` field.
The `AllocateFrom` is a field which can be populated by the scheduler plugins to write arbitrary mapping regarding which 
resource to use to satisfy a given `Request`.

Pod requests which need to specify `Requests` can construct  a `PodInfo` object and marshal it to a JSON structure to write into the
`KubeDevice/DeviceInfo` annotation field. For example, in a pod specification, one can write,
```
apiVersion: v1
kind: Pod
metadata:
  name: tfpod
  annotations:
    KubeDevice/DeviceInfo: {"runningcontainer":{"tfcont":{"requests":{"gpu/gpu-generate-topology":1}}}}
spec:
  containers:
  - name: tfcont
    image: tensorflow/tensorflow:latest-py3-jupyter
    resources:
      requests:
        cpu: 1
      limits:
        nvidia.com/gpu: 2
```

## Scheduler plugin

A scheduler plugin implements the interface specified in
[https://github.com/Microsoft/KubeDevice-API/blob/master/pkg/devicescheduler/devicescheduler.go].
The following interface methods must be implemented by a scheduler plugin
```
type DeviceScheduler interface {
	// add node and resources
	AddNode(nodeName string, nodeInfo *types.NodeInfo)
	// remove node
	RemoveNode(nodeName string)
	// see if pod fits on node & return device score
	PodFitsDevice(nodeInfo *types.NodeInfo, podInfo *types.PodInfo, fillAllocateFrom bool) (bool, []PredicateFailureReason, float64)
	// allocate resources
	PodAllocate(nodeInfo *types.NodeInfo, podInfo *types.PodInfo) error
	// take resources from node
	TakePodResources(*types.NodeInfo, *types.PodInfo) error
	// return resources to node
	ReturnPodResources(*types.NodeInfo, *types.PodInfo) error
	// GetName returns the name of a device
	GetName() string
	// Tells whether group scheduler is being used?
	UsingGroupScheduler() bool
}
```
Here is an explanation of each method.
1. `AddNode(nodeName string, nodeInfo *types.NodeInfo)`
This method is called when a node has been added to the scheduler.  You can use this to configure
any information your plugin needs when a node is added.
Note, you do not need to cache nodeinfo inside the plugin as it will be passed to other
functions as well.  However, if you need to keep track of the "best node" for example, then you can do
something when this function is called.
2. `RemoveNode(nodeName string)`
Similar to AddNode, this function is called when a node is removed from the scheduler.
3. `PodFitsDevice(nodeInfo *types.NodeInfo, podInfo *types.PodInfo, fillAllocateFrom bool) (bool, []PredicateFailureReason, float64)`
You can use information in NodeInfo and PodInfo to determine if the pod will fit on a given node, i.e.
constraints specified in podInfo are being met.
If `fillAllocateFrom` is true, only then do you need to fill the `AllocateFrom` field.
4. `PodAllocate(nodeInfo *types.NodeInfo, podInfo *types.PodInfo) error`
This function is called when you are allocating devices for a given `podInfo` onto a given `nodeInfo`, that is once
the node has been decided.
5. `TakePodResources(*types.NodeInfo, *types.PodInfo) error`
This function is called after a pod has been assigned to a given node.  You can use this to update the
`Used` field for example.
6. `ReturnPodResources(*types.NodeInfo, *types.PodInfo) error`
This function is called when a pod is removed from the cluster.  You can use this to update the `Used` field.
7. `GetName() string`
Returns the name of the scheduler plugin
8. `UsingGroupScheduler() bool`
Returns if the common group scheduler is used. For example, if you do not wish to implement your own logic 
to take care of `Used`, `AllocateFrom`, `PodFitsDevice`, and `PodAllocate`, you can simply use the group scheduler.

To build a scheduler plugin, you must provide a main package which implements the following function
```
func CreateDeviceSchedulerPlugin() (devicescheduler.DeviceScheduler, error)
```
The, plugin should be build using the following
```
go build --buildmode=plugin -o <pluginname>.so <plugin_main_package.go>
```
For an example, take a look at
[https://github.com/Microsoft/KubeGPU/blob/master/Makefile]

## CRI plugin

A CRI plugin implements the interface specified in
[https://github.com/Microsoft/KubeDevice-API/blob/master/pkg/device/device.go], given by the following definition
```
type Device interface {
	// New creates the device and initializes it
	New() error
	// Start logically initializes the device
	Start() error
	// UpdateNodeInfo - updates a node info structure by writing capacity, allocatable, used, scorer
	UpdateNodeInfo(*types.NodeInfo) error
	// Allocate attempts to allocate the devices
	// Returns list of Mounts, and list of Devices to use
	// Returns an error on failure.
	Allocate(*types.PodInfo, *types.ContainerInfo) ([]Mount, []string, map[string]string, error)
	// GetName returns the name of a device
	GetName() string
}
```
The methods do the following.
1. `New() error`
Simply creates a new device.  Initialization is not done here, and should not usually return an error.
2. `Start() error`
Initialize and start the device plugin.  If an error is returned, the plugin stays in a non-operational state.
3. `UpdateNodeInfo(*types.NodeInfo) error`
Used for device advertisement.  This function should populate the `NodeInfo` with `Capacity`, `Allocatable`, `KubeCap`, and `KubeAlloc` fields,
and optionally the `Scorer` field (for use by the scheduler).
4. `Allocate(*types.PodInfo, *types.ContainerInfo) ([]Mount, []string, map[string]string, error)`
Used by the CRI.  Given information from `PodInfo`, return a list of volume mounts, devices, and environment variables (in that order).
5. `GetName() string`
Returns the name of the CRI plugin.

# Contributing

This project welcomes contributions and suggestions.  Most contributions require you to agree to a
Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us
the rights to use your contribution. For details, visit https://cla.microsoft.com.

When you submit a pull request, a CLA-bot will automatically determine whether you need to provide
a CLA and decorate the PR appropriately (e.g., label, comment). Simply follow the instructions
provided by the bot. You will only need to do this once across all repos using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or
contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.
