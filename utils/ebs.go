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

	paginator := ec2.NewDescribeVolumesPaginator(
		clientPtr,
		&ec2.DescribeVolumesInput{
			Filters: filters,
		},
	)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil, fmt.Errorf("\nerror: %v\n", err)
		}

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
				return nil, fmt.Errorf("\nerror: %v\n", err)
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
	var err error

	if _, err = clientPtr.StopInstances(
		context.TODO(),
		&ec2.StopInstancesInput{
			InstanceIds: *instanceIdsListPtr,
		},
	); err != nil {
		return fmt.Errorf("\nerror: %v\n", err)
	}

	stoppedWaiter := ec2.NewInstanceStoppedWaiter(
		clientPtr,
		func(options *ec2.InstanceStoppedWaiterOptions) {
			options.LogWaitAttempts = false
		},
	)

	if err = stoppedWaiter.Wait(
		context.TODO(),
		&ec2.DescribeInstancesInput{
			InstanceIds: *instanceIdsListPtr,
		},
		15*time.Minute,
	); err != nil {
		return fmt.Errorf("\nerror: %v\n", err)
	}

	return nil
}

func StartInstances(clientPtr *ec2.Client, instanceIdsListPtr *[]string) error {
	var err error

	if _, err = clientPtr.StartInstances(
		context.TODO(),
		&ec2.StartInstancesInput{
			InstanceIds: *instanceIdsListPtr,
		},
	); err != nil {
		return fmt.Errorf("\nerror: %v\n", err)
	}

	runningWaiter := ec2.NewInstanceRunningWaiter(
		clientPtr,
		func(options *ec2.InstanceRunningWaiterOptions) {
			options.LogWaitAttempts = false
		},
	)

	if err = runningWaiter.Wait(
		context.TODO(),
		&ec2.DescribeInstancesInput{
			InstanceIds: *instanceIdsListPtr,
		},
		15*time.Minute,
	); err != nil {
		return fmt.Errorf("\nerror: %v\n", err)
	}

	return nil
}

func CreateSnapshot(clientPtr *ec2.Client, volumeId string) (string, error) {
	var createOutput *ec2.CreateSnapshotOutput
	var err error

	if createOutput, err = clientPtr.CreateSnapshot(
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
	); err != nil {
		return "", fmt.Errorf("\nerror: %v\n", err)
	}

	snapshotId := *createOutput.SnapshotId

	var describeOutput *ec2.DescribeSnapshotsOutput

	for {
		if describeOutput, err = clientPtr.DescribeSnapshots(
			context.TODO(),
			&ec2.DescribeSnapshotsInput{
				SnapshotIds: []string{snapshotId},
			},
		); err != nil {
			return "", fmt.Errorf("\nerror: %v\n", err)
		}

		if describeOutput.Snapshots[0].State == types.SnapshotStateCompleted {
			break
		}

		time.Sleep(30 * time.Second)
	}

	return snapshotId, nil
}

func DeleteSnapshot(clientPtr *ec2.Client, snapshotId string) error {
	if _, err := clientPtr.DeleteSnapshot(
		context.TODO(),
		&ec2.DeleteSnapshotInput{SnapshotId: aws.String(snapshotId)},
	); err != nil {
		return fmt.Errorf("\nerror: %v\n", err)
	}

	return nil
}

func CreateVolumeFromSnapshot(clientPtr *ec2.Client, volume *Volume, snapshotId string) (*Volume, error) {
	var createOutput *ec2.CreateVolumeOutput
	var err error

	if createOutput, err = clientPtr.CreateVolume(
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
	); err != nil {
		return nil, fmt.Errorf("\nerror: %v\n", err)
	}

	autoEnableIoAttr := types.AttributeBooleanValue{Value: aws.Bool(volume.MultiAttachEnabled)}

	if _, err = clientPtr.ModifyVolumeAttribute(
		context.TODO(),
		&ec2.ModifyVolumeAttributeInput{
			VolumeId:     createOutput.VolumeId,
			AutoEnableIO: &autoEnableIoAttr,
		},
	); err != nil {
		return nil, fmt.Errorf("\nerror: %v\n", err)
	}

	newVolume := Volume{
		VolumeId:           *createOutput.VolumeId,
		Name:               *createOutput.Tags[0].Value,
		VolumeType:         string(createOutput.VolumeType),
		Zone:               *createOutput.AvailabilityZone,
		State:              string(createOutput.State),
		Iops:               int32(*createOutput.Iops),
		MultiAttachEnabled: *createOutput.MultiAttachEnabled,
		AutoEnableIO:       volume.MultiAttachEnabled,
		Size:               int32(*createOutput.Size),
		Attachments: 		[]Attachment{},
	}

	return &newVolume, nil
}

func DeleteVolume(clientPtr *ec2.Client, volume *Volume) error {
	if _, err := clientPtr.DeleteVolume(
		context.TODO(),
		&ec2.DeleteVolumeInput{
			VolumeId: aws.String(volume.VolumeId),
		},
	); err != nil {
		return fmt.Errorf("\nerror: %v\n", err)
	}

	return nil
}

func WaitForVolumeState(clientPtr *ec2.Client, volumeId *string, volumeState types.VolumeState) error {
	for {
		fmt.Fprintf(os.Stderr, "\nWaiting for volume %s state to change to '%s'...", *volumeId, volumeState)
		describeOutput, err := clientPtr.DescribeVolumes(
			context.TODO(),
			&ec2.DescribeVolumesInput{
				VolumeIds: []string{*volumeId},
			},
		)

		if err != nil {
			return fmt.Errorf("\nerror: %v\n", err)
		}

		if describeOutput.Volumes[0].State == volumeState {
			break
		}

		time.Sleep(15 * time.Second)
	}

	return nil
}

func ReplaceVolumeAttachments(clientPtr *ec2.Client, volumePtr *Volume, newVolumePtr *Volume) error {
	var err error

	for _, attachment := range volumePtr.Attachments {
		fmt.Fprintf(os.Stderr, "\nDetaching volume %s from instance %s...", volumePtr.VolumeId, attachment.InstanceId)

		if _, err = clientPtr.DetachVolume(
			context.TODO(),
			&ec2.DetachVolumeInput{
				VolumeId:   aws.String(volumePtr.VolumeId),
				InstanceId: aws.String(attachment.InstanceId),
				Device:     aws.String(attachment.Device),
			},
		); err != nil {
			return fmt.Errorf("\nerror: %v\n", err)
		}
	}

	if err = WaitForVolumeState(clientPtr, aws.String(volumePtr.VolumeId), types.VolumeStateAvailable); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nUnencrypted volume %s is now '%s'.", volumePtr.VolumeId, types.VolumeStateAvailable)

	for _, attachment := range volumePtr.Attachments {
		fmt.Fprintf(os.Stderr, "\nAttaching volume %s to instance %s...", newVolumePtr.VolumeId, attachment.InstanceId)
		if _, err = clientPtr.AttachVolume(
			context.TODO(),
			&ec2.AttachVolumeInput{
				VolumeId:   aws.String(newVolumePtr.VolumeId),
				InstanceId: aws.String(attachment.InstanceId),
				Device:     aws.String(attachment.Device),
			},
		); err != nil {
			return fmt.Errorf("\nerror: %v\n", err)
		}
	}

	if err = WaitForVolumeState(clientPtr, aws.String(newVolumePtr.VolumeId), types.VolumeStateInUse); err != nil {
		return err
	}
	
	fmt.Fprintf(os.Stderr, "\nEncrypted volume %s is now '%s'.", newVolumePtr.VolumeId, types.VolumeStateInUse)

	return nil
}
