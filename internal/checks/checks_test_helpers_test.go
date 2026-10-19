package checks

import "github.com/soylic/pgtuner/internal/osinspect"

// fakeOS is a test-only Inspector with canned values. Missing sysctl keys return
// ErrNoOSFacts, as does a zero env, effMem, effCPUs, or empty thp.
type fakeOS struct {
	mem    osinspect.Memory
	memErr error
	sysctl map[string]string

	env                osinspect.Environment
	effMem             uint64
	effMemConstrained  bool
	effCPUs            float64
	effCPUsConstrained bool
	thp                string
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
