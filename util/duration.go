package util

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Duration is a wrapper around time.Duration that implements unmarshaling from toml/json
type Duration time.Duration

func (d *Duration) UnmarshalText(data []byte) error {
	// migration: if no unit given assume seconds
	id, err := strconv.ParseInt(string(data), 10, 64)
	if err == nil {
		*d = Duration(id) * Duration(time.Second)
		return nil
	}

	dur, err := time.ParseDuration(string(data))
	if err != nil {
		return fmt.Errorf("parseDuration: %w", err)
	}
	*d = Duration(dur)
	return nil
}

func (d Duration) String() string {
	return (time.Duration)(d).String()
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Duration) UnmarshalJSON(b []byte) (err error) {
	if b[0] == '"' {
		sd := string(b[1 : len(b)-1])
		return d.UnmarshalText([]byte(sd))
	}

	var id int64
	id, err = json.Number(string(b)).Int64()
	if err != nil {
		return fmt.Errorf("parseDuration: %w", err)
	}
	*d = Duration(id)
	return nil
}

func (d Duration) MarshalJSON() (b []byte, err error) {
	return []byte(fmt.Sprintf(`"%s"`, d.String())), nil
}
