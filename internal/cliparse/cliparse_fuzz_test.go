package cliparse

import (
	"strings"
	"testing"
)

func FuzzValidate(f *testing.F) {
	f.Add("--port 8080 --verbose", "port,int;verbose,bool")
	f.Add("--unknown-flag value", "port,int")
	f.Add("--help", "")
	f.Add("", "name,string,required")
	f.Add("--name hello --count 5", "name,string,required;count,int")
	f.Add("--dur 5s", "dur,duration")

	f.Fuzz(func(t *testing.T, rawArgs, flagDefs string) {
		args := strings.Fields(rawArgs)

		// Build a spec from flagDefs: "name,type[,required];..."
		var flags []Flag
		if flagDefs != "" {
			for part := range strings.SplitSeq(flagDefs, ";") {
				fields := strings.Split(part, ",")
				if len(fields) < 2 || fields[0] == "" {
					continue
				}
				fl := Flag{Name: fields[0], Type: fields[1]}
				if len(fields) >= 3 && fields[2] == "required" {
					fl.Required = true
				}
				flags = append(flags, fl)
			}
		}

		spec := &Spec{Name: "test", Flags: flags}
		params, _ := ParseArgs(args)

		// Must not panic.
		_ = Validate(args, params, spec)
	})
}
