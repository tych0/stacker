package stacker

import (
	"github.com/anuvu/stacker/lib"
	"github.com/anuvu/stacker/types"
)

// StackerDepsDAG processes the dependencies between different stacker recipes
type StackerFilesDAG struct {
	dag lib.Graph
}

// NewStackerDepsDAG properly initializes a StackerDepsProcessor
func NewStackerFilesDAG(sfMap types.StackerFiles) (*StackerFilesDAG, error) {
	dag := lib.NewDAG()

	// Add vertices to dag
	for path, sf := range sfMap {
		// Add a vertex for every StackerFile object
		err := dag.AddVertex(path, sf)
		if err != nil {
			return nil, err
		}
	}

	// Update the dependencies in the dag
	for path, sf := range sfMap {
		prerequisites, err := sf.Prerequisites()
		if err != nil {
			return nil, err
		}

		for _, depPath := range prerequisites {
			err := dag.AddDependencies(path, depPath)
			if err != nil {
				return nil, err
			}
		}
	}

	p := StackerFilesDAG{
		dag: dag,
	}
	return &p, nil
}

func (d *StackerFilesDAG) GetStackerFile(path string) *types.Stackerfile {
	value := d.dag.GetValue(path)
	return value.(*types.Stackerfile)
}

// Sort provides a serial build order for the stacker files
func (d *StackerFilesDAG) Sort() []string {
	var order []string

	// Use dag.Sort() to ensure we always process targets in order of their dependencies
	for _, i := range d.dag.Sort() {
		path := i.Key.(string)
		order = append(order, path)
	}

	return order
}
