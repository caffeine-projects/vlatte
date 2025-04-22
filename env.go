package vlatte

import "os"

type Env struct {
	AllowedList map[string]struct{}
}

func (e *Env) Get(k string) string {
	if _, ok := e.AllowedList[k]; ok {
		return os.Getenv(k)
	}

	return ""
}

func (e *Env) GetOr(k string, d string) string {
	if _, ok := e.AllowedList[k]; ok {
		if v := os.Getenv(k); len(v) > 0 {
			return v
		}
	}

	return d
}
