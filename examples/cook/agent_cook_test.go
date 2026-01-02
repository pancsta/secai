package cook

import (
	"testing"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/stretchr/testify/assert"
)

func TestSortSteps(t *testing.T) {
	schema := am.Schema{
		"0": {
			Tags: []string{
				"idx:0",
			},
		},
		"1": {
			Remove: S{"2", "3"},
			Tags: []string{
				"idx:1",
			},
		},
		"2": {
			Remove: S{"1", "3"},
			Tags: []string{
				"idx:1",
			},
		},
		"3": {
			Remove: S{"1", "2"},
			Tags: []string{
				"idx:1",
				"final",
			},
		},
		"4": {
			Tags: []string{
				"idx:2",
			},
		},
	}
	names := sortSteps(schema)
	assert.Equal(t, S{"0", "1", "2", "3", "4"}, names)
}
