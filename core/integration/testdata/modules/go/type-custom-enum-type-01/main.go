package main

// Enum for Status
type Status string

const (
	// Active status
	Active Status = "ACTIVE value"

	// Inactive status
	Inactive Status = "INACTIVE value"

	// Weird status
	WEIRD Status = "WEIRD"
)

func New(
	// +default="INACTIVE value"
	status Status,
) *Test {
	return &Test{Status: status}
}

type Test struct {
	Status Status
}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}

func (m *Test) FromStatusOpt(
	// +optional
	status Status,
) string {
	return string(status)
}

func (m *Test) ToStatus(status string) Status {
	return Status(status)
}
