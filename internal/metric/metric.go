package metric

import "sort"

type Label struct{ Name, Value string }

type Sample struct {
	Name      string
	Labels    []Label
	Value     float64
	Timestamp int64
}

func (s Sample) SortedLabels() []Label {
	out := append([]Label(nil), s.Labels...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
