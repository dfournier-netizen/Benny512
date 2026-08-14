package params

import (
	"context"
	"fmt"

	"benny512/internal/rdm"
)

// SensorReading pairs one sensor's static definition (range, normal band,
// unit/prefix, description — cached, since report §1.1's "learned, not
// per-call" rationale applies just as well to sensors: the definition is a
// firmware-scoped constant) with its live value, so the UI can draw a gauge
// from a single struct without a second lookup (report §1.2).
type SensorReading struct {
	Definition rdm.SensorDefinition
	Value      rdm.SensorValue
}

// InNormalBand reports whether the reading's present value falls inside the
// definition's normal band, when declared — report §1.2's gauge-warning
// signal. Returns true (no warning) when the band isn't declared, since
// "no declared band" isn't itself a fault condition.
func (r SensorReading) InNormalBand() bool {
	if !r.Definition.HasNormalBand() {
		return true
	}
	return r.Value.Present >= r.Definition.NormalMin && r.Value.Present <= r.Definition.NormalMax
}

// sensorDefinitions returns this UID's sensor definitions, fetching and
// caching them (one SENSOR_DEFINITION GET per index) the first time, per
// report §1.2 ("definition... firmware-scoped, cache it"). count comes from
// DEVICE_INFO.SensorCount; a mismatched cached length triggers a refetch
// (covers a device whose sensor count changed, e.g. after a personality
// change on some fixtures).
func (c *Client) sensorDefinitions(ctx context.Context, count byte) ([]rdm.SensorDefinition, error) {
	st := stateFor(c.uid)
	st.mu.RLock()
	cached := st.sensorDefs
	st.mu.RUnlock()
	if len(cached) == int(count) {
		return cached, nil
	}

	defs := make([]rdm.SensorDefinition, 0, count)
	for i := byte(0); i < count; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		data, err := c.getRaw(ctx, rdm.PIDSensorDefinition, rdm.EncodeSensorNumberRequest(i))
		if err != nil {
			return nil, fmt.Errorf("params: SENSOR_DEFINITION[%d]: %w", i, err)
		}
		d, err := rdm.DecodeSensorDefinition(data)
		if err != nil {
			return nil, fmt.Errorf("params: SENSOR_DEFINITION[%d]: %w", i, err)
		}
		defs = append(defs, d)
	}
	st.mu.Lock()
	st.sensorDefs = defs
	st.mu.Unlock()
	return defs, nil
}

// SensorValue issues GET SENSOR_VALUE for one sensor index.
func (c *Client) SensorValue(ctx context.Context, sensorNumber byte) (rdm.SensorValue, error) {
	data, err := c.getRaw(ctx, rdm.PIDSensorValue, rdm.EncodeSensorNumberRequest(sensorNumber))
	if err != nil {
		return rdm.SensorValue{}, err
	}
	return rdm.DecodeSensorValue(data)
}

// Sensors enumerates every sensor on this device (via DEVICE_INFO's sensor
// count, then one SENSOR_DEFINITION + one SENSOR_VALUE GET per index) and
// returns the combined reading list, ready for the UI to render as gauges.
func (c *Client) Sensors(ctx context.Context) ([]SensorReading, error) {
	info, err := c.DeviceInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("params: Sensors: %w", err)
	}
	defs, err := c.sensorDefinitions(ctx, info.SensorCount)
	if err != nil {
		return nil, err
	}
	out := make([]SensorReading, 0, len(defs))
	for _, d := range defs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		v, err := c.SensorValue(ctx, d.SensorNumber)
		if err != nil {
			return nil, fmt.Errorf("params: SENSOR_VALUE[%d]: %w", d.SensorNumber, err)
		}
		out = append(out, SensorReading{Definition: d, Value: v})
	}
	return out, nil
}

// SensorValues re-GETs live SENSOR_VALUE for every already-known sensor
// (from a prior Sensors call) without re-fetching definitions — the cheap
// path for a poll timer (report task item: "poll live sensors...only for
// devices whose detail panel is open").
func (c *Client) SensorValues(ctx context.Context) ([]SensorReading, error) {
	st := stateFor(c.uid)
	st.mu.RLock()
	defs := st.sensorDefs
	st.mu.RUnlock()
	if defs == nil {
		return c.Sensors(ctx)
	}
	out := make([]SensorReading, 0, len(defs))
	for _, d := range defs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		v, err := c.SensorValue(ctx, d.SensorNumber)
		if err != nil {
			return nil, fmt.Errorf("params: SENSOR_VALUE[%d]: %w", d.SensorNumber, err)
		}
		out = append(out, SensorReading{Definition: d, Value: v})
	}
	return out, nil
}

// RecordSensors issues SET RECORD_SENSORS for one sensor index (or
// rdm.AllSensors for every sensor at once), snapshotting the sensor's
// current present_value into its "recorded" slot.
//
// WEAKLY CONFIRMED semantics (report §3.2): the precise verb distinction
// between this ("record now") and SET SENSOR_VALUE ("reset history to
// current") was reconstructed from PID naming convention, not primary
// text — flagged for a hardware sanity check (set, then GET SENSOR_VALUE,
// confirm "recorded" moved to match present_value at the time of the SET).
func (c *Client) RecordSensors(ctx context.Context, sensorNumber byte) error {
	return c.setRaw(ctx, rdm.PIDRecordSensors, rdm.EncodeSensorNumberRequest(sensorNumber))
}

// ResetSensors issues SET SENSOR_VALUE for one sensor index (or
// rdm.AllSensors), which per report §3.2 resets that sensor's
// lowest/highest/recorded history — distinct from RecordSensors above.
func (c *Client) ResetSensors(ctx context.Context, sensorNumber byte) error {
	return c.setRaw(ctx, rdm.PIDSensorValue, rdm.EncodeSensorNumberRequest(sensorNumber))
}
