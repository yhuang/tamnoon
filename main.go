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

func Remediate(clientPtr *ec2.Client, volumesListPtr *[]utils.Volume) error {
	var err error

	for _, volume := range *volumesListPtr {
		instanceIdsList := []string{}

		for _, attachment := range volume.Attachments {
			instanceId := attachment.InstanceId

			if instanceId == "" {
				continue
			}

			instanceIdsList = append(instanceIdsList, instanceId)
		}

		if err = utils.StopInstances(clientPtr, &instanceIdsList); err != nil {
			return err
		}

		snapshotId, err := utils.CreateSnapshot(clientPtr, volume.VolumeId)

		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\nSnapshot %s created.", snapshotId)

		var newVolumePtr *utils.Volume

		if newVolumePtr, err = utils.CreateVolumeFromSnapshot(clientPtr, &volume, snapshotId); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\nCreated encrypted volume %s from snapshot %s.", newVolumePtr.VolumeId, snapshotId)

		if err = utils.ReplaceVolumeAttachments(clientPtr, &volume, newVolumePtr); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\nRedirected unencrypted volume %s attachments to encrypted volume %s.", volume.VolumeId, newVolumePtr.VolumeId)

		if err = utils.DeleteVolume(clientPtr, &volume); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\nDeleted unencrypted volume %s.", volume.VolumeId)

		if err = utils.DeleteSnapshot(clientPtr, snapshotId); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\nSnapshot %s deleted.", snapshotId)

		if err = utils.StartInstances(clientPtr, &instanceIdsList); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\nReplaced unencrypted volume %s with encrypted volume %s.\n", volume.VolumeId, newVolumePtr.VolumeId)
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
