// SPDX-FileCopyrightText: 2025 The Kepler Authors
// SPDX-License-Identifier: Apache-2.0

package monitor

import (
	"maps"

	"github.com/sustainable-computing-io/kepler/internal/resource"
)

// firstVMRead initializes VM power data for the first time
func (pm *PowerMonitor) firstVMRead(snapshot *Snapshot) error {
	running := pm.resources.VirtualMachines().Running
	vms := make(VirtualMachines, len(running))

	zones := snapshot.Node.Zones
	nodeCPUTimeDelta := pm.resources.Node().ProcessTotalCPUTimeDelta

	for id, vm := range running {
		vmInstance := newVM(vm, zones)

		// Calculate initial energy based on CPU ratio * nodeActiveEnergy
		for zone, nodeZoneUsage := range zones {
			if nodeZoneUsage.ActivePower == 0 || nodeZoneUsage.activeEnergy == 0 || nodeCPUTimeDelta == 0 {
				continue
			}

			cpuTimeRatio := vm.CPUTimeDelta / nodeCPUTimeDelta
			activeEnergy := Energy(cpuTimeRatio * float64(nodeZoneUsage.activeEnergy))

			vmInstance.Zones[zone] = Usage{
				Power:       Power(0), // No power in first read - no delta time to calculate rate
				EnergyTotal: activeEnergy,
			}
		}

		vms[id] = vmInstance
	}
	snapshot.VirtualMachines = vms

	pm.logger.Debug("Initialized VM power tracking",
		"vms", len(vms))
	return nil
}

// calculateVMPower calculates power for each running VM and handles terminated VMs
func (pm *PowerMonitor) calculateVMPower(prev, newSnapshot *Snapshot) error {
	vms := pm.resources.VirtualMachines()

	// Copy existing terminated VMs from previous snapshot if not exported
	if !pm.exported.Load() {
		// NOTE: no need to deep clone since already terminated VMs won't be updated
		maps.Copy(newSnapshot.TerminatedVirtualMachines, prev.TerminatedVirtualMachines)
	}

	// Handle terminated VMs
	pm.logger.Debug("Processing terminated VMs", "terminated", len(vms.Terminated))
	for id := range vms.Terminated {
		prevVM, exists := prev.VirtualMachines[id]
		if !exists {
			continue
		}

		// Only include terminated VMs that have consumed energy
		if prevVM.Zones.HasZeroEnergy() {
			pm.logger.Debug("Filtering out terminated VM with zero energy", "id", id)
			continue
		}
		pm.logger.Debug("Including terminated VM with non-zero energy", "id", id)

		terminatedVM := prevVM.Clone()
		newSnapshot.TerminatedVirtualMachines[id] = terminatedVM
	}

	nodeCPUTimeDelta := pm.resources.Node().ProcessTotalCPUTimeDelta
	pm.logger.Debug("Calculating VM power",
		"node.cpu.time", nodeCPUTimeDelta,
		"running", len(vms.Running),
	)

	// Initialize VM map
	vmMap := make(VirtualMachines, len(vms.Running))

	// For each VM, calculate power for each zone separately
	for id, vm := range vms.Running {
		newVMInstance := newVM(vm, newSnapshot.Node.Zones)

		// For each zone in the node, calculate VM's share
		for zone, nodeZoneUsage := range newSnapshot.Node.Zones {
			// Skip zones with zero power to avoid division by zero
			if nodeZoneUsage.ActivePower == 0 || nodeZoneUsage.activeEnergy == 0 || nodeCPUTimeDelta == 0 {
				continue
			}

			// Calculate VM's share of this zone's power and energy
			cpuTimeRatio := vm.CPUTimeDelta / nodeCPUTimeDelta

			// Calculate energy delta for this interval
			activeEnergy := Energy(cpuTimeRatio * float64(nodeZoneUsage.activeEnergy))

			// Calculate absolute energy based on previous data
			absoluteEnergy := activeEnergy
			if prev, exists := prev.VirtualMachines[id]; exists {
				if prevUsage, hasZone := prev.Zones[zone]; hasZone {
					absoluteEnergy += prevUsage.EnergyTotal
				}
			}

			newVMInstance.Zones[zone] = Usage{
				Power:       Power(cpuTimeRatio * nodeZoneUsage.ActivePower.MicroWatts()),
				EnergyTotal: absoluteEnergy,
			}
		}

		vmMap[id] = newVMInstance
	}

	newSnapshot.VirtualMachines = vmMap

	pm.logger.Debug("snapshot updated for VMs",
		"running", len(newSnapshot.VirtualMachines),
		"terminated", len(newSnapshot.TerminatedVirtualMachines))

	return nil
}

// newVM creates a new VirtualMachine struct with initialized zones from resource.VirtualMachine
func newVM(vm *resource.VirtualMachine, zones NodeZoneUsageMap) *VirtualMachine {
	newVMInstance := &VirtualMachine{
		ID:           vm.ID,
		Name:         vm.Name,
		Hypervisor:   vm.Hypervisor,
		CPUTotalTime: vm.CPUTotalTime,
		Zones:        make(ZoneUsageMap, len(zones)),
	}

	// Initialize each zone with zero values
	for zone := range zones {
		newVMInstance.Zones[zone] = Usage{
			EnergyTotal: Energy(0),
			Power:       Power(0),
		}
	}

	return newVMInstance
}
