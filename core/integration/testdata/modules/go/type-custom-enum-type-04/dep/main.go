package main

// Enum for Status
type Status string

const (
	// Active status
	Active Status = "ACTIVE value"

	// Inactive status
	Inactive Status = "INACTIVE value"
)

type Dep struct{}

func (m *Dep) Active() Status {
	return Active
}

func (m *Dep) Inactive() Status {
	return Inactive
}

func (m *Dep) Invert(status Status) Status {
	switch status {
	case Active:
		return Inactive
	case Inactive:
		return Active
	default:
		panic("invalid status")
	}
}
