package sqlite

import (
	sqldriver "database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("mdd_json_contains_all", 2, jsonCollectionMatch(true))
	sqlite.MustRegisterDeterministicScalarFunction("mdd_json_contains_any", 2, jsonCollectionMatch(false))
}

func jsonCollectionMatch(requireAll bool) func(*sqlite.FunctionContext, []sqldriver.Value) (sqldriver.Value, error) {
	return func(_ *sqlite.FunctionContext, values []sqldriver.Value) (sqldriver.Value, error) {
		document, ok := values[0].(string)
		if !ok && values[0] != nil {
			return nil, errors.New("invalid JSON collection")
		}
		candidates, ok := values[1].(string)
		if !ok {
			return nil, errors.New("invalid JSON candidates")
		}
		matched, err := collectionMatches(document, candidates, requireAll)
		if err != nil {
			return nil, err
		}
		if matched {
			return int64(1), nil
		}
		return int64(0), nil
	}
}

func decodeJSON(data string) (any, error) {
	if data == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

// collectionMatches compares JSON array elements without losing numeric precision.
func collectionMatches(document, candidates string, requireAll bool) (bool, error) {
	decode := func(text string) ([]any, error) {
		if text == "" {
			return nil, nil
		}
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var values []any
		if err := decoder.Decode(&values); err != nil {
			return nil, err
		}
		return values, nil
	}
	values, err := decode(document)
	if err != nil {
		return false, fmt.Errorf("invalid JSON collection: %w", err)
	}
	wanted, err := decode(candidates)
	if err != nil {
		return false, fmt.Errorf("invalid JSON candidates: %w", err)
	}
	for _, target := range wanted {
		found := false
		for _, value := range values {
			switch v := value.(type) {
			case json.Number:
				if n, ok := target.(json.Number); ok {
					a, aok := new(big.Rat).SetString(string(v))
					b, bok := new(big.Rat).SetString(string(n))
					found = aok && bok && a.Cmp(b) == 0
				}
			case string:
				t, ok := target.(string)
				found = ok && v == t
			case bool:
				t, ok := target.(bool)
				found = ok && v == t
			case nil:
				found = target == nil
			}
			if found {
				break
			}
		}
		if found != requireAll {
			return found, nil
		}
	}
	return requireAll, nil
}
