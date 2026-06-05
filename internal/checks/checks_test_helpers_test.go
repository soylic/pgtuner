package checks

import "github.com/OpenRemedy/pgtuner/internal/osinspect"

// fakeOS is a test-only Inspector that returns canned values. Sysctl keys not
// present in the map return ErrNoOSFacts, mirroring NoopInspector behaviour for
// unknown keys.
//
// New fields for environment/resource awareness:
//   - env: the Environment to return from Environment(). A zero-value EnvKind
//     ("") signals "not configured" and causes Environment() to return ErrNoOSFacts.
//   - effMem / effMemConstrained: values returned by EffectiveMemoryBytes().
//     When effMem == 0, EffectiveMemoryBytes() returns ErrNoOSFacts.
//   - effCPUs: value returned by EffectiveCPUs(). When effCPUs == 0,
//     EffectiveCPUs() returns ErrNoOSFacts.
//   - thp: string returned by TransparentHugepages(). When "", returns ErrNoOSFacts.
type fakeOS struct {
	mem    osinspect.Memory
	memErr error
	sysctl map[string]string

	// Environment / resource-awareness fields.
	env               osinspect.Environment
	effMem            uint64
	effMemConstrained bool
	effCPUs           float64
	effCPUsConstrained bool
	thp               string
}

func (f fakeOS) Memory() (osinspect.Memory, error) { return f.mem, f.memErr }

func (f fakeOS) Sysctl(k string) (string, error) {
	v, ok := f.sysctl[k]
	if !ok {
		return "", osinspect.ErrNoOSFacts
	}
	return v, nil
}

func (f fakeOS) Environment() (osinspect.Environment, error) {
	if f.env.Kind == "" {
		return osinspect.Environment{}, osinspect.ErrNoOSFacts
	}
	return f.env, nil
}

func (f fakeOS) EffectiveMemoryBytes() (uint64, bool, error) {
	if f.effMem == 0 {
		return 0, false, osinspect.ErrNoOSFacts
	}
	return f.effMem, f.effMemConstrained, nil
}

func (f fakeOS) EffectiveCPUs() (float64, bool, error) {
	if f.effCPUs == 0 {
		return 0, false, osinspect.ErrNoOSFacts
	}
	return f.effCPUs, f.effCPUsConstrained, nil
}

func (f fakeOS) TransparentHugepages() (string, error) {
	if f.thp == "" {
		return "", osinspect.ErrNoOSFacts
	}
	return f.thp, nil
}

func (f fakeOS) StorageDevices() ([]osinspect.StorageDevice, error) {
	return nil, osinspect.ErrNoOSFacts
}
