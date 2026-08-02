package instructions

import (
	"encoding/json"
	"strings"

	"github.com/xymorphic/morph/pkg/str"
)

// Instruction represents a single named instruction with a value string.
type Instruction struct {
	Name  string
	Value string
}

// Instructions is a slice of Instruction objects.
type Instructions []Instruction

// First returns the first Instruction in the slice, or an empty Instruction if there are none.
func (i Instructions) First() Instruction {
	if len(i) == 0 {
		return Instruction{}
	}

	return i[0]
}

// New returns an Instructions slice from a variadic list of string values,
// treating each string as the Value of a new Instruction with an empty Name.
func New(values ...string) Instructions {
	instructions := make(Instructions, 0, len(values))
	for _, value := range values {
		instructions = instructions.AppendValue(value)
	}

	return instructions
}

// Append adds one or more Instruction objects to the current Instructions slice.
// It trims whitespace from Name and Value, and skips empty Value instructions.
func (i Instructions) Append(instructions ...Instruction) Instructions {
	appended := make(Instructions, 0, len(i)+len(instructions))
	appended = append(appended, i...)
	for _, instruction := range instructions {
		value := str.String(instruction.Value)
		if value.Trim() == "" {
			continue
		}
		nameValue := str.String(instruction.Name)
		value2 := str.String(instruction.Value)
		appended = append(appended, Instruction{
			Name:  nameValue.Trim(),
			Value: value2.Trim(),
		})
	}

	return appended
}

// AppendValue creates new Instructions from a variadic list of string values and appends them.
func (i Instructions) AppendValue(values ...string) Instructions {
	instructions := make([]Instruction, 0, len(values))
	for _, value := range values {
		instructions = append(instructions, Instruction{Value: value})
	}

	return i.Append(instructions...)
}

// String returns the concatenated Value fields of all Instructions, separated by double newlines.
func (i Instructions) String() string {
	values := make([]string, 0, len(i))
	for _, instruction := range i {
		values = append(values, instruction.Value)
	}

	return strings.Join(values, "\n\n")
}

// MarshalJSON marshals the Instructions as a JSON string representing the concatenated values.
func (i Instructions) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.String())
}

// UnmarshalJSON unmarshals from a JSON array of strings, creating Instructions with
// empty Names and the strings as Values.
func (i *Instructions) UnmarshalJSON(data []byte) error {
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*i = make(Instructions, len(values))
	for idx, value := range values {
		(*i)[idx] = Instruction{Value: value}
	}

	return nil
}

// GetByName searches for an Instruction by Name and returns it with true if found.
func (i Instructions) GetByName(name string) (Instruction, bool) {
	nameValue2 := str.String(name)
	name = nameValue2.Trim()
	if name == "" {
		return Instruction{}, false
	}
	for _, instruction := range i {
		if instruction.Name == name {
			return instruction, true
		}
	}

	return Instruction{}, false
}

// WithoutName returns a new Instructions slice excluding any Instruction with the given Name.
func (i Instructions) WithoutName(name string) Instructions {
	nameValue3 := str.String(name)
	name = nameValue3.Trim()
	if name == "" {
		return i
	}
	filtered := make(Instructions, 0, len(i))
	for _, instruction := range i {
		if instruction.Name == name {
			continue
		}

		filtered = append(filtered, instruction)
	}

	return filtered
}

// Set adds, replaces, or removes a named instruction.
func (i Instructions) Set(instruction Instruction) Instructions {
	nameValue4 := str.String(instruction.Name)
	instruction.Name = nameValue4.Trim()
	value3 := str.String(instruction.Value)
	instruction.Value = value3.Trim()

	if instruction.Name == "" {
		if instruction.Value == "" {
			return i
		}

		return append(i, instruction)
	}

	for idx, existing := range i {
		if existing.Name != instruction.Name {
			continue
		}

		if instruction.Value == "" {
			updated := make(Instructions, 0, len(i)-1)
			updated = append(updated, i[:idx]...)
			return append(updated, i[idx+1:]...)
		}

		updated := make(Instructions, len(i))
		copy(updated, i)
		updated[idx] = instruction
		return updated
	}

	if instruction.Value == "" {
		return i
	}

	return append(i, instruction)
}
