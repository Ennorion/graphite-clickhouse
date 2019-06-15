package prometheus

import (
	"net/url"
	"strings"

	"github.com/lomik/graphite-clickhouse/render"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/storage"
)

// SeriesIterator iterates over the data of a time series.
type seriesIterator struct {
	data   *render.Data
	offset int
}

// Series represents a single time series.
type series struct {
	data   *render.Data
	offset int
}

// SeriesSet contains a set of series.
type seriesSet struct {
	data   *render.Data
	offset int
}

var _ storage.SeriesSet = &seriesSet{}

func newSeriesSet(data *render.Data) storage.SeriesSet {
	return &seriesSet{data: data, offset: -1}
}

// Seek advances the iterator forward to the value at or after
// the given timestamp.
func (sit *seriesIterator) Seek(t int64) bool {
	tt := uint32(t / 1000)
	if t%1000 != 0 {
		tt++
	}

	pp := sit.data.Points.List()
	for i := sit.offset; i < len(pp); i++ {
		if pp[i].MetricID != pp[sit.offset].MetricID {
			return false
		}

		if pp[i].Time >= tt {
			sit.offset = i
			return true
		}
	}

	return false
}

// At returns the current timestamp/value pair.
func (sit *seriesIterator) At() (t int64, v float64) {
	p := sit.data.Points.List()[sit.offset]
	return int64(p.Time) * 1000, p.Value
}

// Next advances the iterator by one.
func (sit *seriesIterator) Next() bool {
	pp := sit.data.Points.List()
	if sit.offset >= len(pp)-1 {
		return false
	}
	if pp[sit.offset].MetricID != pp[sit.offset+1].MetricID {
		return false
	}
	sit.offset++
	return true
}

// Err returns the current error.
func (sit *seriesIterator) Err() error { return nil }

// Err returns the current error.
func (ss *seriesSet) Err() error { return nil }

func urlParse(rawurl string) (*url.URL, error) {
	p := strings.IndexByte(rawurl, '?')
	if p < 0 {
		return url.Parse(rawurl)
	}
	m, err := url.Parse(rawurl[p:])
	if m != nil {
		m.Path = rawurl[:p]
	}
	return m, err
}

func (ss *seriesSet) At() storage.Series {
	return &series{data: ss.data, offset: ss.offset}
}

func (ss *seriesSet) Next() bool {
	if ss.offset < 0 {
		ss.offset = 0
		return ss.data != nil && ss.data.Points.Len() > 0
	}

	pp := ss.data.Points.List()
	for i := ss.offset; i < len(pp); i++ {
		if pp[i].MetricID != pp[ss.offset].MetricID {
			ss.offset = i
			return true
		}
	}

	return false
}

// Iterator returns a new iterator of the data of the series.
func (s *series) Iterator() storage.SeriesIterator {
	return &seriesIterator{data: s.data, offset: s.offset}
}

func (s *series) Labels() labels.Labels {
	metricName := s.data.Points.MetricName(s.data.Points.List()[s.offset].MetricID)

	u, err := urlParse(metricName)
	if err != nil {
		return labels.Labels{labels.Label{Name: "__name__", Value: metricName}}
	}

	q := u.Query()
	lb := make(labels.Labels, len(q)+1)
	lb[0].Name = "__name__"
	lb[0].Value = u.Path

	i := 0
	for k, v := range q {
		i++
		lb[i].Name = k
		lb[i].Value = v[0]
	}

	return lb
}
