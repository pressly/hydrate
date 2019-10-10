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

func (ps *paramStore) Hydrate(w io.Writer, r io.Reader, format string, k8s bool) error {
	switch format {
	case "json":
		dec := json.NewDecoder(r)
		var data map[string]interface{}
		if err := dec.Decode(&data); err != nil {
			return errors.Wrap(err, "failed to decode JSON")
		}
		if err := ps.hydrateData(data, k8s); err != nil {
			return err
		}
		enc := json.NewEncoder(w)
		if err := enc.Encode(data); err != nil {
			return errors.Wrap(err, "failed to encode JSON")
		}

	case "yml", "yaml":
		dec := yaml.NewDecoder(r)
		enc := yaml.NewEncoder(w)

		// Support multiple YAML documents within a single file.
		for {
			var data map[string]interface{}
			if err := dec.Decode(&data); err != nil {
				if err == io.EOF { // Last document.
					break
				}
				return errors.Wrap(err, "failed to decode YAML")
			}
			if err := ps.hydrateData(data, k8s); err != nil {
				return err
			}
			if err := enc.Encode(data); err != nil {
				return errors.Wrap(err, "failed to encode YAML")
			}
		}

	case "toml":
		var data map[string]interface{}
		b, err := ioutil.ReadAll(r)
		if err != nil {
			return errors.Wrap(err, "failed to read TOML data")
		}
		if err := toml.Unmarshal(b, &data); err != nil {
			return errors.Wrap(err, "failed to decode TOML")
		}
		if err := ps.hydrateData(data, k8s); err != nil {
			return err
		}
		enc := toml.NewEncoder(w)
		if err := enc.Encode(data); err != nil {
			return errors.Wrap(err, "failed to encode TOML")
		}

	default:
		return fmt.Errorf("failed to hydrate: unknown file format %q", format)
	}

	return nil
}
