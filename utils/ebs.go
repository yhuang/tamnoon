package utils

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func GetUnencryptedVolumes(clientPtr *ec2.Client) (*[]Volume, error) {
	volumes := []Volume{}
	filters := []types.Filter{
		{
			Name:   aws.String("encrypted"),
			Values: []string{"false"},
		},
		{
			Name:   aws.String("status"),
			Values: []string{"available", "in-use"},
		},
	}

	// Create a DescribeVolumesPaginator
	paginator := ec2.NewDescribeVolumesPaginator(
		clientPtr,
		&ec2.DescribeVolumesInput{
			Filters: filters,
		},
	)

	// Iterate through each page of results
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil, fmt.Errorf("error retrieving volumes: %v", err)
		}

		// Process the retrieved volumes
		for _, volume := range output.Volumes {
			var attachments []Attachment
			for _, attachment := range volume.Attachments {
				attachments = append(attachments, Attachment{
					InstanceId: *attachment.InstanceId,
					Device:     *attachment.Device,
				})
			}

			attributeOutput, err := clientPtr.DescribeVolumeAttribute(
				context.TODO(),
				&ec2.DescribeVolumeAttributeInput{
					VolumeId:  volume.VolumeId,
					Attribute: types.VolumeAttributeNameAutoEnableIO,
				},
			)

			if err != nil {
				return nil, fmt.Errorf("error retrieving volumes: %v", err)
			}

			volumes = append(
				volumes,
				Volume{
					VolumeId:           *volume.VolumeId,
					Name:               *volume.Tags[0].Value,
					VolumeType:         string(volume.VolumeType),
					Zone:               *volume.AvailabilityZone,
					State:              string(volume.State),
					Iops:               int32(*volume.Iops),
					MultiAttachEnabled: *volume.MultiAttachEnabled,
					AutoEnableIO:       *attributeOutput.AutoEnableIO.Value,
					Size:               int32(*volume.Size),
					Attachments:        attachments,
				},
			)
		}
	}

	return &volumes, nil
}

func StopInstances(clientPtr *ec2.Client, instanceIdsListPtr *[]string) error {
	_, err := clientPtr.StopInstances(
		context.TODO(),
		&ec2.StopInstancesInput{
			InstanceIds: *instanceIdsListPtr,
		},
	)

	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	stoppedWaiter := ec2.NewInstanceStoppedWaiter(
		clientPtr,
		func(options *ec2.InstanceStoppedWaiterOptions) {
			options.LogWaitAttempts = false
		},
	)

	err = stoppedWaiter.Wait(
		context.TODO(),
		&ec2.DescribeInstancesInput{
			InstanceIds: *instanceIdsListPtr,
		},
		15*time.Minute,
	)

	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	return nil
}

func StartInstances(clientPtr *ec2.Client, instanceIdsListPtr *[]string) error {
	_, err := clientPtr.StartInstances(
		context.TODO(),
		&ec2.StartInstancesInput{
			InstanceIds: *instanceIdsListPtr,
		},
	)

	if err != nil {
		panic(err)
	}

	runningWaiter := ec2.NewInstanceRunningWaiter(
		clientPtr,
		func(options *ec2.InstanceRunningWaiterOptions) {
			options.LogWaitAttempts = false
		},
	)

	err = runningWaiter.Wait(
		context.TODO(),
		&ec2.DescribeInstancesInput{
			InstanceIds: *instanceIdsListPtr,
		},
		15*time.Minute,
	)

	if err != nil {
		panic(err)
	}

	return nil
}

func CreateSnapshot(clientPtr *ec2.Client, volumeId string) (string, error) {
	createOutput, err := clientPtr.CreateSnapshot(
		context.TODO(),
		&ec2.CreateSnapshotInput{
			VolumeId: aws.String(volumeId),
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeSnapshot,
					Tags: []types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(volumeId),
						},
					},
				},
			},
		},
	)

	if err != nil {
		return "", fmt.Errorf("error: %v", err)
	}

	snapshotId := *createOutput.SnapshotId

	for {
		describeOutput, err := clientPtr.DescribeSnapshots(
			context.TODO(),
			&ec2.DescribeSnapshotsInput{
				SnapshotIds: []string{snapshotId},
			},
		)

		if err != nil {
			return "", fmt.Errorf("error: %v", err)
		}

		if describeOutput.Snapshots[0].State == types.SnapshotStateCompleted {
			break
		}

		time.Sleep(30 * time.Second)
	}

	return snapshotId, nil
}

func DeleteSnapshot(clientPtr *ec2.Client, snapshotId string) error {
	_, err := clientPtr.DeleteSnapshot(
		context.TODO(),
		&ec2.DeleteSnapshotInput{SnapshotId: aws.String(snapshotId)},
	)

	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	return nil
}

func ReplaceVolumeAttachments(clientPtr *ec2.Client, volume *Volume, snapshotId string) error {
	createOutput, err := clientPtr.CreateVolume(
		context.TODO(),
		&ec2.CreateVolumeInput{
			SnapshotId:         aws.String(snapshotId),
			VolumeType:         types.VolumeType(volume.VolumeType),
			AvailabilityZone:   aws.String(volume.Zone),
			Iops:               aws.Int32(volume.Iops),
			Size:               aws.Int32(volume.Size),
			MultiAttachEnabled: aws.Bool(volume.MultiAttachEnabled),
			Encrypted:          aws.Bool(true),
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeVolume,
					Tags: []types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(fmt.Sprintf("%s (encrypted)", volume.Name)),
						},
					},
				},
			},
		},
	)

	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	autoEnableIoAttr := types.AttributeBooleanValue{Value: aws.Bool(volume.MultiAttachEnabled)}

	_, err = clientPtr.ModifyVolumeAttribute(
		context.TODO(),
		&ec2.ModifyVolumeAttributeInput{
			VolumeId:     createOutput.VolumeId,
			AutoEnableIO: &autoEnableIoAttr,
		},
	)

	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	for _, attachment := range volume.Attachments {
		_, err := clientPtr.DetachVolume(
			context.TODO(),
			&ec2.DetachVolumeInput{
				VolumeId:   aws.String(volume.VolumeId),
				InstanceId: aws.String(attachment.InstanceId),
				Device:     aws.String(attachment.Device),
			},
		)

		if err != nil {
			return fmt.Errorf("error: %v", err)
		}

		fmt.Fprintf(os.Stderr, "\nDetached %s", volume.VolumeId)

		_, err = clientPtr.AttachVolume(
			context.TODO(),
			&ec2.AttachVolumeInput{
				VolumeId:   createOutput.VolumeId,
				InstanceId: aws.String(attachment.InstanceId),
				Device:     aws.String(attachment.Device),
			},
		)

		if err != nil {
			return fmt.Errorf("error: %v", err)
		}

		fmt.Fprintf(os.Stderr, "\nAttached %s", volume.VolumeId)
	}

	// _, err = clientPtr.DeleteVolume(
	// 	context.TODO(),
	// 	&ec2.DeleteVolumeInput{
	// 		VolumeId: aws.String(volume.VolumeId),
	// 	},
	// )

	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	return nil
}
