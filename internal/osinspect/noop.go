package osinspect

// NoopInspector reports no OS facts. pgtuner uses it when it cannot inspect the host.
type NoopInspector struct{}

func (NoopInspector) Memory() (Memory, error)       { return Memory{}, ErrNoOSFacts }
func (NoopInspector) Sysctl(string) (string, error) { return "", ErrNoOSFacts }

func (NoopInspector) Environment() (Environment, error) { return Environment{}, ErrNoOSFacts }
func (NoopInspector) EffectiveMemoryBytes() (uint64, bool, error) {
	return 0, false, ErrNoOSFacts
}
func (NoopInspector) EffectiveCPUs() (float64, bool, error) { return 0, false, ErrNoOSFacts }
func (NoopInspector) TransparentHugepages() (string, error) { return "", ErrNoOSFacts }
func (NoopInspector) StorageDevices() ([]StorageDevice, error) {
	return nil, ErrNoOSFacts
}
