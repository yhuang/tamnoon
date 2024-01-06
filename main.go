package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	utils "tamnoon/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func getAllRegions(clientPtr *ec2.Client) (*map[string]bool, error) {
	output, err := clientPtr.DescribeRegions(context.TODO(), nil)

	if err != nil {
		return nil, fmt.Errorf("\nerror: %v", err)
	}

	allRegionsMap := make(map[string]bool)

	for _, r := range output.Regions {
		allRegionsMap[*r.RegionName] = true
	}

	return &allRegionsMap, nil
}

func selectRegions(clientPtr *ec2.Client) (*[]string, error) {
	allRegionsMapPtr, err := getAllRegions(clientPtr)

	if err != nil {
		return nil, err
	}

	var regionsOpt string
	var regionsList []string

	flag.StringVar(&regionsOpt, "regions", "", "Regions")
	flag.StringVar(&regionsOpt, "r", "", "Regions")

	flag.Parse()

	if regionsOpt == "" {
		regionsList = append(regionsList, "us-west-2")
	} else {
		regionsList = strings.Split(regionsOpt, ",")
	}

	for _, region := range regionsList {
		if !(*allRegionsMapPtr)[region] {
			return nil, fmt.Errorf("error: '%s' is not a valid region", region)
		}
	}

	return &regionsList, nil
}

func getAttachedInstances(volumePtr *utils.Volume) *[]string {
	instanceIdsList := []string{}

	for _, attachment := range volumePtr.Attachments {
		instanceId := attachment.InstanceId

		if instanceId == "" {
			continue
		}

		instanceIdsList = append(instanceIdsList, instanceId)
	}

	return &instanceIdsList
}

func replaceVolume(clientPtr *ec2.Client, volumePtr *utils.Volume) (*utils.Volume, error) {
	var snapshotId string
	var err error

	if snapshotId, err = utils.CreateSnapshot(clientPtr, volumePtr.VolumeId); err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "\nSnapshot %s created from unencrypted volume %s.", snapshotId, volumePtr.VolumeId)

	var newVolumePtr *utils.Volume

	if newVolumePtr, err = utils.CreateVolumeFromSnapshot(clientPtr, volumePtr, snapshotId); err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "\nCreated encrypted volume %s from snapshot %s.", newVolumePtr.VolumeId, snapshotId)

	if err = utils.ReplaceVolumeAttachments(clientPtr, volumePtr, newVolumePtr); err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "\nRedirected unencrypted volume %s's attachments to encrypted volume %s.", volumePtr.VolumeId, newVolumePtr.VolumeId)

	if err = utils.DeleteSnapshot(clientPtr, snapshotId); err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "\nDeleted Snapshot %s.", snapshotId)

	if err = utils.DeleteVolume(clientPtr, volumePtr); err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "\nDeleted unencrypted volume %s.", volumePtr.VolumeId)

	return newVolumePtr, nil
}

func Remediate(clientPtr *ec2.Client, volumesListPtr *[]utils.Volume) error {
	var err error

	for _, volume := range *volumesListPtr {
		instanceIdsListPtr := getAttachedInstances(&volume)

		if err = utils.StopInstances(clientPtr, instanceIdsListPtr); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\nStopped all instances attached to unencrypted volume %s.", volume.VolumeId)

		var newVolumePtr *utils.Volume

		if newVolumePtr, err = replaceVolume(clientPtr, &volume); err != nil {
			return err
		}

		if err = utils.StartInstances(clientPtr, instanceIdsListPtr); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\nStarted all instances attached to encrypted volume %s.\n", newVolumePtr.VolumeId)
	}

	return nil
}

func main() {
	cfg, err := config.LoadDefaultConfig(
		context.TODO(),
		config.WithSharedConfigProfile("tamnoon"),
	)

	if err != nil {
		log.Fatal(err)
	}

	clientPtr := ec2.NewFromConfig(cfg)

	selectedRegionsListPtr, err := selectRegions(clientPtr)

	if err != nil {
		log.Fatal(err)
	}

	var volumesListPtr *[]utils.Volume

	for _, region := range *selectedRegionsListPtr {
		cfg.Region = *aws.String(region)

		clientPtr = ec2.NewFromConfig(cfg)

		if volumesListPtr, err = utils.GetUnencryptedVolumes(clientPtr); err != nil {
			log.Fatal(err)
		}

		data, _ := json.Marshal(*volumesListPtr)
		fmt.Println(string(data))
	}

	if err = Remediate(clientPtr, volumesListPtr); err != nil {
		log.Fatal(err)
	}
}
