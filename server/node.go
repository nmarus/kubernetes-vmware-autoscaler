package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	apiv1 "k8s.io/api/core/v1"

	"github.com/nmarus/autoscaler/vsphere-autoscaler/constantes"
	"github.com/nmarus/autoscaler/vsphere-autoscaler/types"
	"github.com/nmarus/autoscaler/vsphere-autoscaler/utils"
	"github.com/nmarus/autoscaler/vsphere-autoscaler/vsphere"
	"github.com/golang/glog"
)

// AutoScalerServerNodeState VM state
type AutoScalerServerNodeState int32

// autoScalerServerNodeStateString strings
var autoScalerServerNodeStateString = []string{
	"AutoScalerServerNodeStateNotCreated",
	"AutoScalerServerNodeStateRunning",
	"AutoScalerServerNodeStateStopped",
	"AutoScalerServerNodeStateDeleted",
	"AutoScalerServerNodeStateUndefined",
}

const (
	// AutoScalerServerNodeStateNotCreated not created state
	AutoScalerServerNodeStateNotCreated AutoScalerServerNodeState = 0

	// AutoScalerServerNodeStateRunning running state
	AutoScalerServerNodeStateRunning AutoScalerServerNodeState = 1

	// AutoScalerServerNodeStateStopped stopped state
	AutoScalerServerNodeStateStopped AutoScalerServerNodeState = 2

	// AutoScalerServerNodeStateDeleted deleted state
	AutoScalerServerNodeStateDeleted AutoScalerServerNodeState = 3

	// AutoScalerServerNodeStateUndefined undefined state
	AutoScalerServerNodeStateUndefined AutoScalerServerNodeState = 4
)

// AutoScalerServerNode Describe a AutoScaler VM
type AutoScalerServerNode struct {
	ProviderID       string                    `json:"providerID"`
	NodeGroupID      string                    `json:"group"`
	NodeName         string                    `json:"name"`
	NodeIndex        int                       `json:"index"`
	Memory           int                       `json:"memory"`
	CPU              int                       `json:"cpu"`
	Disk             int                       `json:"disk"`
	Addresses        []string                  `json:"addresses"`
	State            AutoScalerServerNodeState `json:"state"`
	AutoProvisionned bool                      `json:"auto"`
	VSphereConfig    *vsphere.Configuration    `json:"vmware"`
	serverConfig     *types.AutoScalerServerConfig
}

func (s AutoScalerServerNodeState) String() string {
	return autoScalerServerNodeStateString[s]
}

func (vm *AutoScalerServerNode) prepareKubelet() (string, error) {
	var out string
	var err error
	var fName = fmt.Sprintf("/tmp/set-kubelet-default-%s.sh", vm.NodeName)

	kubeletDefault := []string{
		"#!/bin/bash",
		". /etc/default/kubelet",
		fmt.Sprintf("echo \"KUBELET_EXTRA_ARGS=\\\"$KUBELET_EXTRA_ARGS --provider-id=%s\\\"\" > /etc/default/kubelet", vm.ProviderID),
		"systemctl restart kubelet",
	}

	if err = ioutil.WriteFile(fName, []byte(strings.Join(kubeletDefault, "\n")), 0755); err != nil {
		return out, err
	}

	defer os.Remove(fName)

	if err = utils.Scp(vm.serverConfig.SSH, vm.Addresses[0], fName, fName); err != nil {
		glog.Errorf("Unable to scp node %s address:%s, reason:%s", vm.Addresses[0], vm.NodeName, err)

		return out, err
	}

	if out, err = utils.Sudo(vm.serverConfig.SSH, vm.Addresses[0], fmt.Sprintf("bash %s", fName)); err != nil {
		glog.Errorf("Unable to ssh node %s address:%s, reason:%s", vm.Addresses[0], vm.NodeName, err)

		return out, err
	}

	return "", nil
}

func (vm *AutoScalerServerNode) waitReady() error {
	glog.V(5).Infof("AutoScalerNode::waitReady, node:%s", vm.NodeName)

	kubeconfig := vm.serverConfig.KubeConfig

	// Max 60s
	for index := 0; index < 12; index++ {
		var out string
		var err error
		var arg = []string{
			"kubectl",
			"get",
			"nodes",
			vm.NodeName,
			"--output",
			"json",
			"--kubeconfig",
			kubeconfig,
		}

		if out, err = utils.Pipe(arg...); err != nil {
			return err
		}

		var nodeInfo apiv1.Node

		if err = json.Unmarshal([]byte(out), &nodeInfo); err != nil {
			return fmt.Errorf(constantes.ErrUnmarshallingError, vm.NodeName, err)
		}

		for _, status := range nodeInfo.Status.Conditions {
			if status.Type == "Ready" {
				if b, e := strconv.ParseBool(string(status.Status)); e == nil {
					if b {
						glog.Infof("The kubernetes node %s is Ready", vm.NodeName)
						return nil
					}
				}
			}
		}

		glog.Infof("The kubernetes node:%s is not ready", vm.NodeName)

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf(constantes.ErrNodeIsNotReady, vm.NodeName)
}

func (vm *AutoScalerServerNode) kubeAdmJoin() error {
	kubeAdm := vm.serverConfig.KubeAdm

	args := []string{
		"kubeadm",
		"join",
		kubeAdm.Address,
		"--token",
		kubeAdm.Token,
		"--discovery-token-ca-cert-hash",
		kubeAdm.CACert,
	}

	// Append extras arguments
	if len(kubeAdm.ExtraArguments) > 0 {
		args = append(args, kubeAdm.ExtraArguments...)
	}

	// glog.Infof("Running '%s' on node %s", strings.Join(args, " "), vm.NodeName)

	if _, err := utils.Sudo(vm.serverConfig.SSH, vm.Addresses[0], strings.Join(args, " ")); err != nil {
		return err
	}

	return nil
}

func (vm *AutoScalerServerNode) setNodeLabels(nodeLabels, systemLabels KubernetesLabel) error {
	if len(nodeLabels)+len(systemLabels) > 0 {

		args := []string{
			"kubectl",
			"label",
			"nodes",
			vm.NodeName,
		}

		// Append extras arguments
		for k, v := range nodeLabels {
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}

		for k, v := range systemLabels {
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}

		args = append(args, "--kubeconfig")
		args = append(args, vm.serverConfig.KubeConfig)

		if out, err := utils.Pipe(args...); err != nil {
			return fmt.Errorf(constantes.ErrKubeCtlReturnError, vm.NodeName, out, err)
		}
	}

	args := []string{
		"kubectl",
		"annotate",
		"node",
		vm.NodeName,
		fmt.Sprintf("%s=%s", constantes.NodeLabelGroupName, vm.NodeGroupID),
		fmt.Sprintf("%s=%s", constantes.AnnotationNodeAutoProvisionned, strconv.FormatBool(vm.AutoProvisionned)),
		fmt.Sprintf("%s=%d", constantes.AnnotationNodeIndex, vm.NodeIndex),
		"--overwrite",
		"--kubeconfig",
		vm.serverConfig.KubeConfig,
	}

	if out, err := utils.Pipe(args...); err != nil {
		return fmt.Errorf(constantes.ErrKubeCtlReturnError, vm.NodeName, out, err)
	}

	return nil
}

var phDefaultRsyncFlags = []string{
	"--verbose",
	"--archive",
	"-z",
	"--copy-links",
	"--no-owner",
	"--no-group",
	"--delete",
}

func (vm *AutoScalerServerNode) syncFolders() (string, error) {

	syncFolders := vm.serverConfig.SyncFolders

	if syncFolders != nil && len(syncFolders.Folders) > 0 {
		for _, folder := range syncFolders.Folders {
			var rsync = []string{
				"rsync",
			}

			tempFile, _ := ioutil.TempFile(os.TempDir(), "vmware-rsync")

			defer tempFile.Close()

			if len(syncFolders.RsyncOptions) == 0 {
				rsync = append(rsync, phDefaultRsyncFlags...)
			} else {
				rsync = append(rsync, syncFolders.RsyncOptions...)
			}

			sshOptions := []string{
				"--rsync-path",
				"sudo rsync",
				"-e",
				fmt.Sprintf("ssh -p 22 -o LogLevel=FATAL -o ControlMaster=auto -o ControlPath=%s -o ControlPersist=10m  -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i '%s'", tempFile.Name(), syncFolders.RsyncSSHKey),
			}

			excludes := make([]string, 0, len(folder.Excludes)*2)

			for _, exclude := range folder.Excludes {
				excludes = append(excludes, "--exclude", exclude)
			}

			rsync = append(rsync, sshOptions...)
			rsync = append(rsync, excludes...)
			rsync = append(rsync, folder.Source, fmt.Sprintf("%s@%s:%s", syncFolders.RsyncUser, vm.Addresses[0], folder.Destination))

			if out, err := utils.Pipe(rsync...); err != nil {
				return out, err
			}
		}
	}
	//["/usr/bin/rsync", "--verbose", "--archive", "--delete", "-z", "--copy-links", "--no-owner", "--no-group", "--rsync-path", "sudo rsync", "-e",
	// "ssh -p 22 -o LogLevel=FATAL  -o ControlMaster=auto -o ControlPath=/tmp/vagrant-rsync-20181227-31508-1sjw4bm -o ControlPersist=10m  -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i '/home/fboltz/.ssh/id_rsa'",
	// "--exclude", ".vagrant/", "/home/fboltz/Projects/vagrant-multipass/", "vagrant@10.196.85.125:/vagrant"]
	return "", nil
}

func (vm *AutoScalerServerNode) launchVM(nodeLabels, systemLabels KubernetesLabel) error {
	glog.V(5).Infof("AutoScalerNode::launchVM, node:%s", vm.NodeName)

	var err error
	var status AutoScalerServerNodeState
	var output string

	vsphere := vm.VSphereConfig
	network := vsphere.Network
	userInfo := vm.serverConfig.SSH

	glog.Infof("Launch VM:%s for nodegroup: %s", vm.NodeName, vm.NodeGroupID)

	if vm.AutoProvisionned == false {

		err = fmt.Errorf(constantes.ErrVMNotProvisionnedByMe, vm.NodeName)

	} else if vm.State != AutoScalerServerNodeStateNotCreated {

		err = fmt.Errorf(constantes.ErrVMAlreadyCreated, vm.NodeName)

	} else if _, err = vsphere.Create(vm.NodeName, userInfo.GetUserName(), userInfo.GetAuthKeys(), vm.serverConfig.CloudInit, network, "", vm.Memory, vm.CPU, vm.Disk); err != nil {

		err = fmt.Errorf(constantes.ErrUnableToLaunchVM, vm.NodeName, err)

	} else if err = vsphere.PowerOn(vm.NodeName); err != nil {

		err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

	} else if _, err = vsphere.WaitForToolsRunning(vm.NodeName); err != nil {

		err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

	} else if _, err = vsphere.WaitForIP(vm.NodeName); err != nil {

		err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

	} else if status, err = vm.statusVM(); err != nil {

		err = fmt.Errorf(constantes.ErrGetVMInfoFailed, vm.NodeName, err)

	} else if status != AutoScalerServerNodeStateRunning {

		err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

	} else if output, err = vm.syncFolders(); err != nil {

		err = fmt.Errorf(constantes.ErrRsyncError, vm.NodeName, output, err)

	} else if output, err = vm.prepareKubelet(); err != nil {

		err = fmt.Errorf(constantes.ErrKubeletNotConfigured, vm.NodeName, output, err)

	} else if err = vm.kubeAdmJoin(); err != nil {

		err = fmt.Errorf(constantes.ErrKubeAdmJoinFailed, vm.NodeName, err)

	} else if err = vm.waitReady(); err != nil {

		err = fmt.Errorf(constantes.ErrNodeIsNotReady, vm.NodeName)

	} else {
		err = vm.setNodeLabels(nodeLabels, systemLabels)
	}

	if err == nil {
		glog.Infof("Launched VM:%s for nodegroup: %s", vm.NodeName, vm.NodeGroupID)
	} else {
		glog.Errorf("Unable to launch VM:%s for nodegroup: %s. Reason: %v", vm.NodeName, vm.NodeGroupID, err.Error())
	}

	return err
}

func (vm *AutoScalerServerNode) startVM() error {
	glog.V(5).Infof("AutoScalerNode::startVM, node:%s", vm.NodeName)

	var err error
	var state AutoScalerServerNodeState

	glog.Infof("Start VM:%s", vm.NodeName)

	kubeconfig := vm.serverConfig.KubeConfig
	vsphere := vm.VSphereConfig

	if vm.AutoProvisionned == false {

		err = fmt.Errorf(constantes.ErrVMNotProvisionnedByMe, vm.NodeName)

	} else if state, err = vm.statusVM(); err != nil {

		err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

	} else if state == AutoScalerServerNodeStateStopped {

		if err = vsphere.PowerOn(vm.NodeName); err != nil {

			err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

		} else if _, err = vsphere.WaitForIP(vm.NodeName); err != nil {

			err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

		} else if state, err = vm.statusVM(); err != nil {

			err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

		} else if state != AutoScalerServerNodeStateRunning {

			err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, err)

		} else {
			args := []string{
				"kubectl",
				"uncordon",
				vm.NodeName,
				"--kubeconfig",
				kubeconfig,
			}

			if err = utils.Shell(args...); err != nil {
				glog.Errorf(constantes.ErrKubeCtlIgnoredError, vm.NodeName, err)

				err = nil
			}

			vm.State = AutoScalerServerNodeStateRunning
		}
	} else if state != AutoScalerServerNodeStateRunning {
		err = fmt.Errorf(constantes.ErrStartVMFailed, vm.NodeName, fmt.Sprintf("Unexpected state: %d", state))
	}

	if err == nil {
		glog.Infof("Started VM:%s", vm.NodeName)
	} else {
		glog.Errorf("Unable to start VM:%s. Reason: %v", vm.NodeName, err)
	}

	return err
}

func (vm *AutoScalerServerNode) stopVM() error {
	glog.V(5).Infof("AutoScalerNode::stopVM, node:%s", vm.NodeName)

	var err error
	var state AutoScalerServerNodeState

	glog.Infof("Stop VM:%s", vm.NodeName)

	kubeconfig := vm.serverConfig.KubeConfig
	vsphere := vm.VSphereConfig

	if vm.AutoProvisionned == false {

		err = fmt.Errorf(constantes.ErrVMNotProvisionnedByMe, vm.NodeName)

	} else if state, err = vm.statusVM(); err != nil {

		err = fmt.Errorf(constantes.ErrStopVMFailed, vm.NodeName, err)

	} else if state == AutoScalerServerNodeStateRunning {

		args := []string{
			"kubectl",
			"cordon",
			vm.NodeName,
			"--kubeconfig",
			kubeconfig,
		}

		if err = utils.Shell(args...); err != nil {
			glog.Errorf(constantes.ErrKubeCtlIgnoredError, vm.NodeName, err)
		}

		if err = vsphere.PowerOff(vm.NodeName); err == nil {
			vm.State = AutoScalerServerNodeStateStopped
		} else {
			err = fmt.Errorf(constantes.ErrStopVMFailed, vm.NodeName, err)
		}

	} else if state != AutoScalerServerNodeStateStopped {

		err = fmt.Errorf(constantes.ErrStopVMFailed, vm.NodeName, fmt.Sprintf("Unexpected state: %d", state))

	}

	if err == nil {
		glog.Infof("Stopped VM:%s", vm.NodeName)
	} else {
		glog.Errorf("Could not stop VM:%s. Reason: %s", vm.NodeName, err)
	}

	return err
}

func (vm *AutoScalerServerNode) deleteVM() error {
	glog.V(5).Infof("AutoScalerNode::deleteVM, node:%s", vm.NodeName)

	var err error
	var status *vsphere.Status

	if vm.AutoProvisionned == false {
		err = fmt.Errorf(constantes.ErrVMNotProvisionnedByMe, vm.NodeName)
	} else {
		kubeconfig := vm.serverConfig.KubeConfig
		vsphere := vm.VSphereConfig

		if status, err = vsphere.Status(vm.NodeName); err == nil {
			if status.Powered {
				args := []string{
					"kubectl",
					"drain",
					vm.NodeName,
					"--delete-local-data",
					"--force",
					"--ignore-daemonsets",
					"--kubeconfig",
					kubeconfig,
				}

				if err = utils.Shell(args...); err != nil {
					glog.Errorf(constantes.ErrKubeCtlIgnoredError, vm.NodeName, err)
				}

				args = []string{
					"kubectl",
					"delete",
					"node",
					vm.NodeName,
					"--kubeconfig",
					kubeconfig,
				}

				if err = utils.Shell(args...); err != nil {
					glog.Errorf(constantes.ErrKubeCtlIgnoredError, vm.NodeName, err)
				}

				if err = vsphere.PowerOff(vm.NodeName); err != nil {
					err = fmt.Errorf(constantes.ErrStopVMFailed, vm.NodeName, err)
				} else {
					vm.State = AutoScalerServerNodeStateStopped

					if err = vsphere.Delete(vm.NodeName); err != nil {
						err = fmt.Errorf(constantes.ErrDeleteVMFailed, vm.NodeName, err)
					}
				}
			} else if err = vsphere.Delete(vm.NodeName); err != nil {
				err = fmt.Errorf(constantes.ErrDeleteVMFailed, vm.NodeName, err)
			}
		}
	}

	if err == nil {
		glog.Infof("Deleted VM:%s", vm.NodeName)
		vm.State = AutoScalerServerNodeStateDeleted
	} else {
		glog.Errorf("Could not delete VM:%s. Reason: %s", vm.NodeName, err)
	}

	return err
}

func (vm *AutoScalerServerNode) statusVM() (AutoScalerServerNodeState, error) {
	glog.V(5).Infof("AutoScalerNode::statusVM, node:%s", vm.NodeName)

	// Get VM infos
	var status *vsphere.Status
	var err error

	if status, err = vm.VSphereConfig.Status(vm.NodeName); err != nil {
		glog.Errorf(constantes.ErrGetVMInfoFailed, vm.NodeName, err)
		return AutoScalerServerNodeStateUndefined, err
	}

	if status != nil {
		vm.Addresses = []string{
			status.Address,
		}

		if status.Powered {
			vm.State = AutoScalerServerNodeStateRunning
		} else {
			vm.State = AutoScalerServerNodeStateStopped
		}

		return vm.State, nil
	}

	return AutoScalerServerNodeStateUndefined, fmt.Errorf(constantes.ErrAutoScalerInfoNotFound, vm.NodeName)
}

func (vm *AutoScalerServerNode) xgetVSphere() *vsphere.Configuration {
	var vsphere *vsphere.Configuration

	if vsphere = vm.serverConfig.VMwareInfos[vm.NodeGroupID]; vsphere == nil {
		vsphere = vm.serverConfig.VMwareInfos["default"]
	}

	if vsphere == nil {
		glog.Fatalf("Unable to find vmware config for node:%s in group", vm.NodeName, vm.NodeGroupID)
	}

	return vsphere
}

func (vm *AutoScalerServerNode) setServerConfiguration(config *types.AutoScalerServerConfig) {
	vm.serverConfig = config
}

// cleanOnLaunchError called when error occurs during launch
func (vm *AutoScalerServerNode) cleanOnLaunchError(err error) {
	glog.Errorf(constantes.ErrUnableToLaunchVM, vm.NodeName, err)

	if status, _ := vm.statusVM(); status != AutoScalerServerNodeStateNotCreated {
		if e := vm.deleteVM(); e != nil {
			glog.Errorf(constantes.ErrUnableToDeleteVM, vm.NodeName, e)
		}
	} else {
		glog.Warningf(constantes.WarnFailedVMNotDeleted, vm.NodeName, status)
	}

}
