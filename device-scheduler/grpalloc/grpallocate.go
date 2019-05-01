package grpalloc

import (
	"regexp"

	sctypes "github.com/Microsoft/KubeDevice-API/pkg/devicescheduler"
	"github.com/Microsoft/KubeDevice-API/pkg/resource"
	"github.com/Microsoft/KubeDevice-API/pkg/types"
	"github.com/Microsoft/KubeDevice-API/pkg/utils"
	"github.com/Microsoft/KubeDevice/device-scheduler/grpalloc/scorer"
)

// ===================================================

func findSubGroups(baseGroup string, grp map[string]string) (map[string](map[string](map[string]string)), map[string]bool) {
	subGrp := make(map[string](map[string](map[string]string)))
	isSubGrp := make(map[string]bool)
	// regex tester for groups
	utils.Logf(5, "Subgroup def %v", baseGroup+`/(\S*?)/(\S*?)/(\S*)`)
	re := regexp.MustCompile(baseGroup + `/(\S*?)/(\S*?)/(\S*)`)
	for grpKey, grpElem := range grp {
		matches := re.FindStringSubmatch(grpElem)
		if len(matches) >= 4 {
			utils.AssignMap(subGrp, matches[1:], grpElem)
			isSubGrp[grpKey] = true
		} else {
			isSubGrp[grpKey] = false
		}
	}
	return subGrp, isSubGrp
}

func printResMap(res map[string]int64, grp map[string]string, isSubGrp map[string]bool) {
	for grpKey, grpElem := range grp {
		utils.Logf(5, "Key %v GlobalKey %v Val %v IsSubGrp %v", grpKey, grpElem, res[grpElem], isSubGrp[grpKey])
	}
}

// =====================================================

// GrpAllocator is the type to perform group allocations
type GrpAllocator struct {
	// Global read info for all
	ContName      string
	InitContainer bool
	PreferUsed    bool
	// required resource and scorer
	RequiredResource map[string]int64
	ReqScorer        map[string]scorer.ResourceScoreFunc
	// allocatable resource and scorer
	AllocResource map[string]int64
	AllocScorer   map[string]scorer.ResourceScoreFunc

	// Global read/write info
	UsedGroups map[string]bool

	// Per group info, read only
	// Resource Info - required
	GrpRequiredResource map[string]string
	IsReqSubGrp         map[string]bool
	// Resource Info - allocatable
	GrpAllocResource map[string](map[string]string)
	IsAllocSubGrp    map[string]bool
	// other nodeinfo
	ReqBaseGroupName     string
	AllocBaseGroupPrefix string

	// Used Info, write as group is explored
	Score        float64
	PodResource  map[string]int64
	NodeResource map[string]int64
	AllocateFrom map[string]string
}

// sub group writes into parent group's AllocateFrom, PodResource, and NodeResource
func (grp *GrpAllocator) createSubGroup(
	resourceLocation string,
	requiredSubGrps map[string](map[string](map[string]string)),
	allocSubGrps map[string](map[string](map[string]string)),
	grpName string,
	grpIndex string) *GrpAllocator {

	subGrp := *grp // shallow copy of struct
	// overwrite
	subGrp.GrpRequiredResource = requiredSubGrps[grpName][grpIndex]
	subGrp.GrpAllocResource = allocSubGrps[grpName]
	if subGrp.GrpAllocResource == nil {
		subGrp.GrpAllocResource = make(map[string](map[string]string))
	}
	subGrp.ReqBaseGroupName = grp.ReqBaseGroupName + "/" + grpName + "/" + grpIndex
	subGrp.AllocBaseGroupPrefix = grp.AllocBaseGroupPrefix + "/" + resourceLocation + "/" + grpName
	subGrp.Score = 0 // reset to zero

	return &subGrp
}

// cloned group takes on new AllocateFrom, PodResource, and NodeResource
func (grp *GrpAllocator) cloneGroup() *GrpAllocator {
	newGrp := *grp
	// overwrite
	newGrp.AllocateFrom = make(map[string]string)
	newGrp.PodResource = make(map[string]int64)
	newGrp.NodeResource = make(map[string]int64)
	if grp.AllocateFrom != nil {
		for key, val := range grp.AllocateFrom {
			newGrp.AllocateFrom[key] = val
		}
	}
	if grp.PodResource != nil {
		for key, val := range grp.PodResource {
			newGrp.PodResource[key] = val
		}
	}
	if grp.NodeResource != nil {
		for key, val := range grp.NodeResource {
			newGrp.NodeResource[key] = val
		}
	}
	newGrp.Score = grp.Score

	return &newGrp
}

func (grp *GrpAllocator) takeGroup(grpTake *GrpAllocator) {
	grp.AllocateFrom = grpTake.AllocateFrom
	grp.PodResource = grpTake.PodResource
	grp.NodeResource = grpTake.NodeResource
	grp.Score = grpTake.Score
}

func (grp *GrpAllocator) resetGroup(restorePoint *GrpAllocator) {
	grp.PodResource = restorePoint.PodResource
	grp.NodeResource = restorePoint.NodeResource
	grp.Score = restorePoint.Score
}

// returns whether resource available and score
// can use hash of scorers for different resources
// simple leftover for now for score, 0 is low, 1.0 is high score
func (grp *GrpAllocator) resourceAvailable(resourceLocation string) (bool, []sctypes.PredicateFailureReason) {
	grpAllocRes := grp.GrpAllocResource[resourceLocation]

	utils.Logf(5, "Resource requirments")
	printResMap(grp.RequiredResource, grp.GrpRequiredResource, grp.IsReqSubGrp)
	utils.Logf(5, "Available in group")
	printResMap(grp.AllocResource, grpAllocRes, grp.IsAllocSubGrp)

	found := true
	var predicateFails []sctypes.PredicateFailureReason
	for grpReqKey, grpReqElem := range grp.GrpRequiredResource {
		if !grp.IsReqSubGrp[grpReqKey] {
			// see if resource exists
			utils.Logf(5, "Testing for resource %v", grpReqElem)
			required := grp.RequiredResource[grpReqElem]
			globalName, available := grpAllocRes[grpReqKey]
			if !available {
				found = false
				predicateFails = append(predicateFails, resource.NewInsufficientResourceError(
					types.ResourceName(grp.ContName+"/"+grpReqElem), required, int64(0), int64(0)))
				continue
			}
			scoreFn := grp.ReqScorer[grpReqElem]
			allocatable := grp.AllocResource[globalName]
			usedPod := grp.PodResource[globalName]
			usedNode := grp.NodeResource[globalName]
			if scoreFn == nil {
				// if req scorer is nil (used to find if resource available), use nodeInfo score function
				scoreFn = grp.AllocScorer[globalName]
			}
			// alternatively, current score can be passed in, and new score returned if score not additive
			foundR, scoreR, _, podR, nodeR := scoreFn(allocatable, usedPod, usedNode, []int64{required}, grp.InitContainer)
			if !foundR {
				found = false
				predicateFails = append(predicateFails, resource.NewInsufficientResourceError(
					types.ResourceName(grp.ContName+"/"+grpReqElem), required, usedNode, allocatable))
				continue
			}
			grp.PodResource[globalName] = podR
			grp.NodeResource[globalName] = nodeR
			grp.AllocateFrom[grpReqElem] = globalName
			utils.Logf(5, "Resource %v Available with score %v", grpReqElem, scoreR)
		} else {
			utils.Logf(5, "No test for subgroup %v", grpReqElem)
		}
	}

	return found, predicateFails
}

// allocate and return
// attempt to allocate for group, and then allocate subgroups
func (grp *GrpAllocator) allocateSubGroups(
	allocLocationName string,
	subgrpsReq map[string](map[string](map[string]string)),
	subgrpsAllocRes map[string](map[string](map[string]string))) (
	bool, []sctypes.PredicateFailureReason) {

	found := true
	var predicateFails []sctypes.PredicateFailureReason
	sortedSubGrpsReqKeys := utils.SortedStringKeys(subgrpsReq)
	for _, subgrpsKey := range sortedSubGrpsReqKeys {
		subgrpsElemGrp := subgrpsReq[subgrpsKey]
		sortedSubgrpsElemGrp := utils.SortedStringKeys(subgrpsElemGrp)
		for _, subgrpsElemIndex := range sortedSubgrpsElemGrp {
			utils.Logf(5, "Allocating subgroup with key %v and index %v", subgrpsKey, subgrpsElemIndex)
			subGrp := grp.createSubGroup(allocLocationName, subgrpsReq, subgrpsAllocRes, subgrpsKey, subgrpsElemIndex)
			foundSubGrp, reasons := subGrp.allocateGroup()
			if !foundSubGrp {
				found = false
				predicateFails = append(predicateFails, resource.NewInsufficientResourceError(
					types.ResourceName(grp.ContName+"/"+subGrp.ReqBaseGroupName), 0, 0, 0))
				predicateFails = append(predicateFails, reasons...)
				continue
			}
			grp.takeGroup(subGrp) // update to current
		}
	}
	return found, predicateFails
}

func (grp *GrpAllocator) findScoreAndUpdate(location string) (bool, []sctypes.PredicateFailureReason) {
	found := true
	var predicateFails []sctypes.PredicateFailureReason

	// first compute list of requested resources to alloctable resources
	requestedResource := make(map[string]([]int64))
	for _, grpReqElem := range grp.GrpRequiredResource {
		allocFrom := string(grp.AllocateFrom[grpReqElem]) // may return "" if not available, but okay since next will return not available
		_, available := grp.AllocResource[allocFrom]
		if !available {
			found = false
			predicateFails = append(predicateFails,
				resource.NewInsufficientResourceError(types.ResourceName(grpReqElem), grp.RequiredResource[grpReqElem], int64(0), int64(0)))
			continue
		}
		requestedResource[allocFrom] = append(requestedResource[allocFrom], grp.RequiredResource[grpReqElem])
	}

	// now perform scoring over alloctable resources
	grp.Score = 0.0
	for _, key := range grp.GrpAllocResource[location] {
		allocatable := grp.AllocResource[key]
		scoreFn := grp.AllocScorer[key]
		usedPod := grp.PodResource[key]
		usedNode := grp.NodeResource[key]
		foundR, scoreR, totalRequest, podR, nodeR := scoreFn(allocatable, usedPod, usedNode, requestedResource[key], grp.InitContainer)
		if !foundR {
			found = false
			predicateFails = append(predicateFails,
				resource.NewInsufficientResourceError(types.ResourceName(key), totalRequest, usedNode, allocatable))
			continue
		}
		grp.Score += scoreR
		grp.PodResource[key] = podR
		grp.NodeResource[key] = nodeR
	}
	lenI := len(grp.GrpAllocResource[location])
	lenF := float64(lenI)
	grp.Score /= lenF

	return found, predicateFails
}

func (grp *GrpAllocator) allocateGroupAt(location string,
	subgrpsReq map[string](map[string](map[string]string))) (bool, []sctypes.PredicateFailureReason) {

	allocLocationName := grp.AllocBaseGroupPrefix + "/" + location
	grpsAllocResElem := grp.GrpAllocResource[location]
	subgrpsAllocRes, isSubGrp := findSubGroups(allocLocationName, grpsAllocResElem)
	grp.IsAllocSubGrp = isSubGrp

	grpR := grp.cloneGroup()
	foundRes, reasons := grp.resourceAvailable(location)
	utils.Logf(4, "group %v base resource available", location)

	// next allocatable subgroups for this location
	foundNext, reasonsNext := grp.allocateSubGroups(location, subgrpsReq, subgrpsAllocRes)
	// find score for group
	if foundRes && foundNext {
		utils.Logf(4, "group %v resource available", location)
		grp.resetGroup(grpR)
		foundScore, reasonsScore := grp.findScoreAndUpdate(location)
		if foundScore == false {
			utils.Errorf("Unable to find allocation during scoring, even though it has already been found %v", reasonsScore)
			foundNext = false
			reasonsNext = append(reasonsNext, reasonsScore...)
		} else {
			utils.Logf(4, "group %v available score %f", location, grp.Score)
		}
	}

	return (foundRes && foundNext), append(reasons, reasonsNext...)
}

// "n" is used to index over list of group resources
//
// "i" is used to index over list of allocatable groups
//
// req is map of requirements
// grpReq is map where key is "group" name of requirement and value is "global" name
// i.e. req[grpReq[n]] is the requirement of "n"th resource in group
//
// allocRes is map of allocatable resources on nodeinfo
// grpsAllocRes is map of groups of allocatable resources
// i.e. allocRes[grpAllocRes[i][n]] is the available resource in the "i"th alloctable group of the "n"th resource
//
// allocated map refers to which resource is being used in allocations
// i.e. allocRes[allocated[grpReq[n]] is the resource used for the "n"th resource in group
//
// usedResource is the amount of utilized resource in the global resource list
// i.e. usedResource[grpAllocRes[i][n]] is used when considering the "i"th alloctable group of the "n"th resource
// i.e. usedResource[allocated[grpReq[n]]] is subtracted from after allocation
func (grp *GrpAllocator) allocateGroup() (bool, []sctypes.PredicateFailureReason) {
	if len(grp.GrpRequiredResource) == 0 {
		return true, nil
	}

	anyFind := false
	maxScoreKey := ""
	maxScoreGrp := grp
	maxIsUsedGroup := false
	maxGroupName := ""
	var predicateFails []sctypes.PredicateFailureReason

	// find subgroups for required resources
	subgrpsReq, isSubGrp := findSubGroups(grp.ReqBaseGroupName, grp.GrpRequiredResource)
	grp.IsReqSubGrp = isSubGrp

	// go over all possible places to allocate
	sortedGrpAllocResourceKeys := utils.SortedStringKeys(grp.GrpAllocResource)
	utils.Logf(7, "All available resources locations: %v", sortedGrpAllocResourceKeys)
	for _, grpsAllocResKey := range sortedGrpAllocResourceKeys {
		grpCheck := grp.cloneGroup()
		found, reasons := grpCheck.allocateGroupAt(grpsAllocResKey, subgrpsReq)
		allocLocationName := grp.AllocBaseGroupPrefix + "/" + grpsAllocResKey

		if found {
			utils.Logf(5, "For resource %v - available at %v with score - %v",
				grp.ReqBaseGroupName, allocLocationName, grpCheck.Score)
			takeNew := false
			if !grp.PreferUsed {
				if grpCheck.Score >= maxScoreGrp.Score {
					takeNew = true
				}
			} else {
				// prefer previously used
				if maxIsUsedGroup {
					// already have used group, only take if current is used and score is higher
					if grp.UsedGroups[allocLocationName] && grpCheck.Score >= maxScoreGrp.Score {
						takeNew = true
					}
				} else {
					// don't have used, take if score higher or used
					if grp.UsedGroups[allocLocationName] || grpCheck.Score >= maxScoreGrp.Score {
						takeNew = true
					}
				}
			}
			if takeNew {
				anyFind = true
				maxScoreKey = grpsAllocResKey
				maxScoreGrp = grpCheck
				maxIsUsedGroup = grp.UsedGroups[allocLocationName]
				maxGroupName = allocLocationName
			}
		} else {
			utils.Logf(5, "For resource %v - not available at %v with score - %v",
				grp.ReqBaseGroupName, allocLocationName, grpCheck.Score)
		}

		if len(grp.GrpAllocResource) == 1 {
			predicateFails = append(predicateFails, reasons...)
		}
	}

	grp.takeGroup(maxScoreGrp) // take the group with max score
	if anyFind {
		utils.Logf(5, "Maxscore from key %v", maxScoreKey)
		grp.UsedGroups[maxGroupName] = true
		return true, nil
	}

	return false, predicateFails
}

// allocate the main group
func containerFitsGroupConstraints(contName string, contReq *types.ContainerInfo, initContainer bool,
	allocatable types.ResourceList, allocScorer map[string]scorer.ResourceScoreFunc,
	podResource map[string]int64, nodeResource map[string]int64,
	usedGroups map[string]bool, bPreferUsed bool, bSetAllocateFrom bool) (
	*GrpAllocator, bool, []sctypes.PredicateFailureReason, float64) {

	grp := &GrpAllocator{}

	// Required resources
	reqName := make(map[string]string)
	req := make(map[string]int64)
	reqScorer := make(map[string]scorer.ResourceScoreFunc)
	// Quantitites available on NodeInfo
	allocName := make(map[string](map[string]string))
	alloc := make(map[string]int64)
	utils.Logf(5, "Allocating for container %v", contName)
	utils.Logf(7, "Requests %v", contReq.DevRequests)
	utils.Logf(7, "AllocatableRes %v", allocatable)
	for reqRes, reqVal := range contReq.DevRequests {
		if !resource.PrecheckedResource(reqRes) {
			reqName[string(reqRes)] = string(reqRes)
			req[string(reqRes)] = reqVal
			scoreEnum, available := contReq.Scorer[reqRes]
			var scoreFn scorer.ResourceScoreFunc
			if available {
				scoreFn = scorer.SetScorer(reqRes, scoreEnum)
			} else {
				scoreFn = nil
			}
			reqScorer[string(reqRes)] = scoreFn
		}
	}
	utils.Logf(7, "Required %v %v", reqName, req)

	re := regexp.MustCompile(`(\S*)/(\S*)`)
	matches := re.FindStringSubmatch(types.DeviceGroupPrefix)
	var grpPrefix string
	var grpName string
	if len(matches) != 3 {
		panic("Invalid prefix")
	} else {
		grpPrefix = matches[1]
		grpName = matches[2]
	}
	for allocRes, allocVal := range allocatable {
		if !resource.PrecheckedResource(allocRes) {
			utils.AssignMap(allocName, []string{grpName, string(allocRes)}, string(allocRes))
			alloc[string(allocRes)] = allocVal
		}
	}
	utils.Logf(7, "Allocatable %v %v", allocName, alloc)

	grp.ContName = contName
	grp.InitContainer = initContainer
	grp.PreferUsed = bPreferUsed
	grp.RequiredResource = req
	grp.ReqScorer = reqScorer
	grp.AllocResource = alloc
	grp.AllocScorer = allocScorer
	grp.UsedGroups = usedGroups
	grp.GrpRequiredResource = reqName
	grp.GrpAllocResource = allocName
	grp.ReqBaseGroupName = types.DeviceGroupPrefix
	grp.AllocBaseGroupPrefix = grpPrefix
	grp.Score = 0.0
	// pick up current resource usage
	grp.PodResource = podResource
	grp.NodeResource = nodeResource

	var found bool
	var reasons []sctypes.PredicateFailureReason
	var score float64

	if contReq.AllocateFrom == nil || (len(contReq.AllocateFrom) == 0 && len(req) > 0) {
		found, reasons = grp.allocateGroup()
		score = grp.Score
		if bSetAllocateFrom {
			contReq.AllocateFrom = make(types.ResourceLocation)
			for allocatedKey, allocatedLocVal := range grp.AllocateFrom {
				contReq.AllocateFrom[types.ResourceName(allocatedKey)] = types.ResourceName(allocatedLocVal)
				utils.Logf(3, "Set allocate from %v to %v", allocatedKey, allocatedLocVal)
			}
		}
	} else if len(req) > 0 {
		utils.Logf(5, "Performing only find and score -- allocatefrom already set")
		// set grp allocate from
		grp.AllocateFrom = make(map[string]string)
		for key, val := range contReq.AllocateFrom {
			grp.AllocateFrom[string(key)] = string(val)
		}
		found, reasons = grp.findScoreAndUpdate(grpName)
		score = grp.Score
	} else {
		// len(req) == 0
		utils.Logf(5, "No group requests")
		grp.AllocateFrom = make(map[string]string)
		found = true
		score = 0.0
	}

	utils.Logf(2, "Allocated %v", grp.AllocateFrom)
	utils.Logf(3, "PodResources %v", grp.PodResource)
	utils.Logf(3, "NodeResources %v", grp.NodeResource)
	utils.Logf(2, "Container allocation found %v with score %v", found, score)

	return grp, found, reasons, score
}

func initNodeResource(n *types.NodeInfo) map[string]int64 {
	utils.Logf(5, "Used resource %v", n.Used)
	nodeResource := make(map[string]int64)
	for resKey, resVal := range n.Used {
		nodeResource[string(resKey)] = resVal
	}
	return nodeResource
}

func PodClearAllocateFrom(spec *types.PodInfo) {
	for contName, contCopy := range spec.RunningContainers {
		contCopy.AllocateFrom = nil
		spec.RunningContainers[contName] = contCopy
	}
	for contName, contCopy := range spec.InitContainers {
		contCopy.AllocateFrom = nil
		spec.InitContainers[contName] = contCopy
	}
}

// set score function for alloctable resource at node
func setScoreFunc(n *types.NodeInfo) map[string]scorer.ResourceScoreFunc {
	scorerFn := make(map[string]scorer.ResourceScoreFunc)
	for key := range n.Allocatable {
		keyS := string(key)
		scorerFn[keyS] = scorer.SetScorer(key, n.Scorer[key])
	}
	return scorerFn
}

// PodFitsGroupConstraints tells if pod fits constraints, score returned is score of running containers
func PodFitsGroupConstraints(n *types.NodeInfo, spec *types.PodInfo, allocating bool) (bool, []sctypes.PredicateFailureReason, float64) {
	podResource := make(map[string]int64)
	nodeResource := initNodeResource(n)
	usedGroups := make(map[string]bool)
	totalScore := 0.0
	var predicateFails []sctypes.PredicateFailureReason
	found := true

	// set score function for alloctable resources at node
	scorer := setScoreFunc(n)

	// first go over running containers
	contKeys := utils.SortedStringKeys(spec.RunningContainers)
	for _, contName := range contKeys {
		contCopy := spec.RunningContainers[contName]
		grp, fits, reasons, score := containerFitsGroupConstraints(contName, &contCopy, false, n.Allocatable,
			scorer, podResource, nodeResource, usedGroups, true, allocating)
		spec.RunningContainers[contName] = contCopy
		if fits == false {
			found = false
			predicateFails = append(predicateFails, reasons...)
		} else {
			//totalScore += score
			totalScore = score // assign to new value as it contains all info
		}
		podResource = grp.PodResource
		nodeResource = grp.NodeResource
	}

	// now go over initialization containers, try to reutilize used groups
	contKeys = utils.SortedStringKeys(spec.InitContainers)
	for _, contName := range contKeys {
		contCopy := spec.InitContainers[contName]
		// container.Resources.DevRequests contains a map, alloctable contains type Resource
		// prefer groups which are already used by running containers
		grp, fits, reasons, _ := containerFitsGroupConstraints(contName, &contCopy, true, n.Allocatable,
			scorer, podResource, nodeResource, usedGroups, true, allocating)
		spec.InitContainers[contName] = contCopy
		if fits == false {
			found = false
			predicateFails = append(predicateFails, reasons...)
		}
		podResource = grp.PodResource
		nodeResource = grp.NodeResource
	}

	utils.Logf(4, "Found: %v Fails: %v totalScore: %v", found, predicateFails, totalScore)
	utils.Logf(4, "Used %v", usedGroups)

	return found, predicateFails, totalScore
}

// Node usage accounting
func updateGroupResourceForContainer(n *types.NodeInfo, cont *types.ContainerInfo, bInitContainer bool,
	podResources types.ResourceList, updatedUsedByNode types.ResourceList) {

	for reqRes, allocatedFrom := range cont.AllocateFrom {
		// only update "group" device resources
		if !resource.PrecheckedResource(reqRes) {
			val := cont.DevRequests[reqRes]
			allocatableRes := n.Allocatable[allocatedFrom]
			podRes := podResources[allocatedFrom]
			nodeRes := updatedUsedByNode[allocatedFrom]
			scorerFn := scorer.SetScorer(allocatedFrom, n.Scorer[allocatedFrom])
			_, _, _, newPodUsed, newNodeUsed := scorerFn(allocatableRes, podRes, nodeRes, []int64{val}, bInitContainer)
			podResources[allocatedFrom] = newPodUsed
			updatedUsedByNode[allocatedFrom] = newNodeUsed
		}
	}
}

// ComputePodGroupResources returns resources needed by pod & updated node resources
func ComputePodGroupResources(n *types.NodeInfo, spec *types.PodInfo, bRemovePod bool) (
	podResources types.ResourceList, updatedUsedByNode types.ResourceList) {

	updatedUsedByNode = make(types.ResourceList)
	podResources = make(types.ResourceList)
	for key, val := range n.Used {
		updatedUsedByNode[key] = val
	}

	// go over running containers to compute utilized resources
	for _, cont := range spec.RunningContainers {
		updateGroupResourceForContainer(n, &cont, false, podResources, updatedUsedByNode)
	}

	// now go over init containers to compute resources required
	for _, cont := range spec.InitContainers {
		updateGroupResourceForContainer(n, &cont, true, podResources, updatedUsedByNode)
	}

	// for pod removal, remove all resources used by pod at end, ignore addition by subtracting from n.Used
	if bRemovePod {
		for allocatedFrom, podUsed := range podResources {
			scorerFn := scorer.SetScorer(allocatedFrom, n.Scorer[allocatedFrom])
			_, _, _, _, newNodeUsed := scorerFn(0, 0, n.Used[allocatedFrom], []int64{-podUsed}, false)
			updatedUsedByNode[allocatedFrom] = newNodeUsed
		}
	}

	utils.Logf(5, "PodGroupResourcesComputes: podResources: %v updateUsedByNode: %v removePod: %v", podResources, updatedUsedByNode, bRemovePod)

	return podResources, updatedUsedByNode
}

// TakePodGroupResource takes pod resource from node, pod added
func TakePodGroupResource(n *types.NodeInfo, spec *types.PodInfo) {
	_, usedResources := ComputePodGroupResources(n, spec, false)

	for usedResourceKey, usedResourceVal := range usedResources {
		n.Used[usedResourceKey] = usedResourceVal
	}
}

// ReturnPodGroupResource returns pod resource to node, pod removed
func ReturnPodGroupResource(n *types.NodeInfo, spec *types.PodInfo) {
	_, usedResources := ComputePodGroupResources(n, spec, true)

	for usedResourceKey, usedResourceVal := range usedResources {
		n.Used[usedResourceKey] = usedResourceVal
	}
}
