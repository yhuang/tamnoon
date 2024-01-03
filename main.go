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
	results, err := clientPtr.DescribeRegions(context.TODO(), nil)

	if err != nil {
		return nil, err
	}

	allRegionsMap := make(map[string]bool)

	for _, r := range results.Regions {
		allRegionsMap[*r.RegionName] = true
	}

	return &allRegionsMap, nil
}

func selectRegions(clientPtr *ec2.Client) (*[]string, error) {
	allRegionsMapPtr, err := getAllRegions(clientPtr)

	if err != nil {
		log.Fatal(err)
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
			return nil, fmt.Errorf("error: \"%s\" is not a valid region", region)
		}
	}

	return &regionsList, nil
}

func Remediate(clientPtr *ec2.Client, volumesPtr *[]utils.Volume) error {
	for _, volume := range *volumesPtr {
		instanceIdsList := []string{}

		for _, attachment := range volume.Attachments {
			instanceId := attachment.InstanceId
			instanceIdsList = append(instanceIdsList, instanceId)
		}

		err := utils.StopInstances(clientPtr, &instanceIdsList)

		if err != nil {
			return fmt.Errorf("error: %v", err)
		}

		snapshotId, err := utils.CreateSnapshot(clientPtr, volume.VolumeId)

		if err != nil {
			return fmt.Errorf("error: %v", err)
		}

		fmt.Fprintf(os.Stderr, "\nSnapshot %s created", snapshotId)

		err = utils.ReplaceVolumeAttachments(clientPtr, &volume, snapshotId)

		if err != nil {
			return fmt.Errorf("error: %v", err)
		}

		err = utils.DeleteSnapshot(clientPtr, snapshotId)

		if err != nil {
			return fmt.Errorf("error: %v", err)
		}

		fmt.Fprintf(os.Stderr, "\nSnapshot %s deleted", snapshotId)

		utils.StartInstances(clientPtr, &instanceIdsList)

		if err != nil {
			return fmt.Errorf("error: %v", err)
		}

		fmt.Fprintf(os.Stderr, "\nVolume %s remediated\n", volume.VolumeId)
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

	var volumesPtr *[]utils.Volume

	for _, region := range *selectedRegionsListPtr {
		cfg.Region = *aws.String(region)

		clientPtr = ec2.NewFromConfig(cfg)
		volumesPtr, err = utils.GetUnencryptedVolumes(clientPtr)

		if err != nil {
			log.Fatal(err)
		}

		data, _ := json.Marshal(*volumesPtr)
		fmt.Println(string(data))
	}

	err = Remediate(clientPtr, volumesPtr)

	if err != nil {
		log.Fatal(err)
	}
}
