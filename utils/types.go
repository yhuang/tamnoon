package utils

type Attachment struct {
	InstanceId string `json:"instanceId"`
	Device     string `json:"device"`
}

type Volume struct {
	VolumeId           string       `json:"volumeId"`
	Name               string       `json:"name"`
	VolumeType         string       `json:"volume-type"`
	Zone               string       `json:"zone"`
	State              string       `json:"state"`
	Iops               int32        `json:"iops"`
	MultiAttachEnabled bool         `json:"multi-attach-enabled"`
	AutoEnableIO       bool         `json:"auto-enable-io"`
	Size               int32        `json:"size"`
	Attachments        []Attachment `json:"attachments"`
}
