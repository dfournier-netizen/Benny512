package patch

// PhaseCountFor gives explicit operator intent precedence over an estimate
// from the selected GDTF mode, then live RDM, then a labelled single slot.
// Counting unique geometry instances per attribute avoids counting RGBW
// components or coarse/fine bytes as extra cells. This is an estimate, not
// a claim that every geometry is an RDM sub-device.
func PhaseCountFor(e Entry, rdmCount uint16) (uint16, string) {
	if e.PhaseCount > 0 {
		return e.PhaseCount, "override"
	}
	groups := map[string]map[string]bool{}
	for _, cf := range e.ChannelFunctions {
		if cf.Source != SourceGDTF || cf.GeometryInstance == "" || cf.Attribute == "" {
			continue
		}
		if groups[cf.Attribute] == nil {
			groups[cf.Attribute] = map[string]bool{}
		}
		groups[cf.Attribute][cf.GeometryInstance] = true
	}
	count := 0
	for _, instances := range groups {
		if len(instances) > count {
			count = len(instances)
		}
	}
	if count > 1 && count <= 512 {
		return uint16(count), "GDTF geometry estimate"
	}
	if rdmCount > 0 {
		return rdmCount, "live RDM sub-devices"
	}
	return 1, "single slot (count unknown)"
}
