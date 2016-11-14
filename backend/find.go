package backend

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gogo/protobuf/proto"
	"github.com/lomik/graphite-clickhouse/carbonzipperpb"
)

type FindHandler struct {
	config *Config
}

func hasWildcard(target string) bool {
	return strings.IndexAny(target, "[]{}*") > -1
}

func makeWhere(target string, withLevel bool) (where string) {
	level := strings.Count(target, ".") + 1

	AND := func(exp string) {
		if where == "" {
			where = exp
		} else {
			where = fmt.Sprintf("(%s) AND %s", where, exp)
		}
	}

	if withLevel {
		where = fmt.Sprintf("Level = %d", level)
	}

	if target == "*" {
		return
	}

	// simple metric
	if !hasWildcard(target) {
		AND(fmt.Sprintf("Path = '%s'", target))
		return
	}

	// before any wildcard symbol
	simplePrefix := target[:strings.IndexAny(target, "[]{}*")]

	if len(simplePrefix) > 0 {
		AND(fmt.Sprintf("Path LIKE '%s%%'", simplePrefix))
	}

	// prefix search like "metric.name.xx*"
	if len(simplePrefix) == len(target)-1 && target[len(target)-1] == '*' {
		return
	}

	pattern := globToRegexp(target)
	AND(fmt.Sprintf("match(Path, '%s')", pattern))

	return
}

func globToRegexp(g string) string {
	s := g
	s = strings.Replace(s, "*", "([^.]*?)", -1)
	s = strings.Replace(s, "{", "(", -1)
	s = strings.Replace(s, "}", ")", -1)
	s = strings.Replace(s, ",", "|", -1)
	return s
}

func RemoveExtraPrefix(prefix, query string) (string, string, error) {
	qs := strings.Split(query, ".")
	ps := strings.Split(prefix, ".")

	var i int
	for i = 0; i < len(qs) && i < len(ps); i++ {
		m, err := regexp.MatchString(globToRegexp(qs[i]), ps[i])
		if err != nil {
			return "", "", err
		}
		if !m { // not matched
			return "", "", nil
		}
	}

	if i < len(ps) {
		return strings.Join(ps[:i], "."), "", nil
	}

	return prefix, strings.Join(qs[i:], "."), nil
}

func (h *FindHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("query")

	if strings.IndexByte(q, '\'') > -1 { // sql injection dumb fix
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var prefix string
	var err error

	if h.config.ClickHouse.ExtraPrefix != "" {
		prefix, q, err = RemoveExtraPrefix(h.config.ClickHouse.ExtraPrefix, q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if q == "" {
			h.Reply(w, r, prefix+".", "")
			return
		}
	}

	where := makeWhere(q, true)

	if where == "" {
		http.Error(w, "Bad or unsupported query", http.StatusBadRequest)
		return
	}

	data, err := Query(
		r.Context(),
		h.config.ClickHouse.Url,
		fmt.Sprintf("SELECT Path FROM %s WHERE %s GROUP BY Path", h.config.ClickHouse.TreeTable, where),
		h.config.ClickHouse.TreeTimeout.Value(),
	)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.Reply(w, r, string(data), prefix)
}

func (h *FindHandler) Reply(w http.ResponseWriter, r *http.Request, chResponse, prefix string) {
	switch r.URL.Query().Get("format") {
	case "pickle":
		h.ReplyPickle(w, r, chResponse, prefix)
	case "protobuf":
		h.ReplyProtobuf(w, r, chResponse, prefix)
	}
}

func (h *FindHandler) ReplyPickle(w http.ResponseWriter, r *http.Request, chResponse, prefix string) {
	rows := strings.Split(string(chResponse), "\n")

	if len(rows) == 0 { // empty
		w.Write(PickleEmptyList)
		return
	}

	p := NewPickler(w)

	p.List()

	var metricPath string
	var isLeaf bool

	for _, metricPath = range rows {
		if len(metricPath) == 0 {
			continue
		}

		if prefix != "" {
			metricPath = prefix + "." + metricPath
		}

		if metricPath[len(metricPath)-1] == '.' {
			metricPath = metricPath[:len(metricPath)-1]
			isLeaf = false
		} else {
			isLeaf = true
		}

		p.Dict()

		p.String("metric_path")
		p.String(metricPath)
		p.SetItem()

		p.String("isLeaf")
		p.Bool(isLeaf)
		p.SetItem()

		p.Append()
	}

	p.Stop()
}

func (h *FindHandler) ReplyProtobuf(w http.ResponseWriter, r *http.Request, chResponse, prefix string) {
	rows := strings.Split(string(chResponse), "\n")

	name := r.URL.Query().Get("query")

	// message GlobMatch {
	//     required string path = 1;
	//     required bool isLeaf = 2;
	// }

	// message GlobResponse {
	//     required string name = 1;
	//     repeated GlobMatch matches = 2;
	// }

	var response carbonzipperpb.GlobResponse
	response.Name = proto.String(name)

	var metricPath string
	var isLeaf bool

	for _, metricPath = range rows {
		if len(metricPath) == 0 {
			continue
		}

		if prefix != "" {
			metricPath = prefix + "." + metricPath
		}

		if metricPath[len(metricPath)-1] == '.' {
			metricPath = metricPath[:len(metricPath)-1]
			isLeaf = false
		} else {
			isLeaf = true
		}

		response.Matches = append(response.Matches, &carbonzipperpb.GlobMatch{
			Path:   proto.String(metricPath),
			IsLeaf: &isLeaf,
		})
	}

	body, _ := proto.Marshal(&response)
	w.Write(body)
}

func NewFindHandler(config *Config) *FindHandler {
	return &FindHandler{
		config: config,
	}
}