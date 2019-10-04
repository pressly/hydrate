package hydrate

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/BurntSushi/toml"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

func GetData(r io.Reader, format string) (map[string]interface{}, error) {
	var data map[string]interface{}

	switch format {
	case "json":
		dec := json.NewDecoder(r)
		if err := dec.Decode(&data); err != nil {
			return nil, errors.Wrap(err, "failed to decode JSON")
		}

	case "yml", "yaml":
		dec := yaml.NewDecoder(r)
		if err := dec.Decode(&data); err != nil {
			return nil, errors.Wrap(err, "failed to decode YAML")
		}

	case "toml":
		b, err := ioutil.ReadAll(r)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read TOML data")
		}
		if err := toml.Unmarshal(b, &data); err != nil {
			return nil, errors.Wrap(err, "failed to decode TOML")
		}

	default:
		return nil, fmt.Errorf("failed to hydrate: unknown file format %q", format)
	}

	return data, nil
}

func PrintData(w io.Writer, data map[string]interface{}, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		if err := enc.Encode(data); err != nil {
			return errors.Wrap(err, "failed to encode JSON")
		}

	case "yml", "yaml":
		enc := yaml.NewEncoder(w)
		if err := enc.Encode(data); err != nil {
			return errors.Wrap(err, "failed to encode YAML")
		}

	case "toml":
		enc := toml.NewEncoder(w)
		if err := enc.Encode(data); err != nil {
			return errors.Wrap(err, "failed to encode TOML")
		}

	default:
		return fmt.Errorf("failed to hydrate: unknown file format %q", format)
	}

	return nil
}
