// Command perfgate compares the speed of two builds of a benchmark suite on
// one machine, for the perf check of pull requests
// (.github/workflows/perf.yml).
//
//	perfgate run -base base.test -head head.test -o samples.json
//	perfgate verdict samples.json
//	perfgate calibrate samples-1.json samples-2.json ...
//
// run alternates the two binaries, one process per case and side, and swaps
// which side goes first every round: pairing a round's two runs cancels the
// drift of a shared runner, and swapping cancels the cost of running second.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Samples is the output of run and the input of verdict and calibrate.
// NsPerOp[case][side][round] has side 0 for the base and 1 for the head, for
// the cases both sides have.
type Samples struct {
	Base, Head string
	CPU        string
	Rounds     int
	Benchtime  string
	NsPerOp    map[string][2][]float64
	OnlyBase   []string `json:",omitempty"`
	OnlyHead   []string `json:",omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "verdict":
		err = cmdVerdict(os.Args[2:], os.Stdout)
	case "calibrate":
		err = cmdCalibrate(os.Args[2:], os.Stdout)
	default:
		usage()
	}
	if errors.Is(err, errRegression) {
		os.Exit(exitRegression)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "perfgate:", err)
		os.Exit(exitError)
	}
}

// Exit statuses. perf.yml lets the perf:accept label override status 1 only,
// so that a failed measurement is never accepted as a regression.
const (
	exitRegression = 1
	exitError      = 2
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: perfgate run|verdict|calibrate [flags] (see the package comment)")
	os.Exit(exitError)
}

var errRegression = errors.New("performance regression")

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var (
		base      = fs.String("base", "", "base test binary")
		head      = fs.String("head", "", "head test binary")
		baseLabel = fs.String("base-label", "base", "label recorded for the base")
		headLabel = fs.String("head-label", "head", "label recorded for the head")
		bench     = fs.String("bench", "^BenchmarkGate$", "top-level benchmark regexp")
		rounds    = fs.Int("rounds", 20, "rounds (each runs every case once per side)")
		benchtime = fs.String("benchtime", "200ms", "-test.benchtime of each run")
		cpu       = fs.Int("cpu", -1, "CPU to pin the benchmarks to with taskset (-1: no pinning)")
		out       = fs.String("o", "samples.json", "output file")
	)
	_ = fs.Parse(args)
	if *base == "" || *head == "" || *rounds < 6 {
		return errors.New("run needs -base, -head and -rounds >= 6")
	}
	bins := [2]string{*base, *head}

	var names [2][]string
	var cpuLine string
	for side, bin := range bins {
		res, cl, err := runBench(bin, *bench, "1x", *cpu)
		if err != nil {
			return err
		}
		cpuLine = cl
		for name := range res {
			names[side] = append(names[side], name)
		}
		slices.Sort(names[side])
	}
	cases, onlyBase, onlyHead := intersect(names[0], names[1])
	if len(cases) == 0 {
		return fmt.Errorf("no benchmark matching %q exists on both sides", *bench)
	}

	s := Samples{Base: *baseLabel, Head: *headLabel, CPU: cpuLine, Rounds: *rounds, Benchtime: *benchtime,
		NsPerOp: map[string][2][]float64{}, OnlyBase: onlyBase, OnlyHead: onlyHead}
	start := time.Now()
	for r := range *rounds {
		for _, c := range cases {
			order := [2]int{0, 1}
			if r%2 == 1 {
				order = [2]int{1, 0}
			}
			for _, side := range order {
				res, _, err := runBench(bins[side], exactPattern(c), *benchtime, *cpu)
				if err != nil {
					return err
				}
				ns, ok := res[c]
				if !ok {
					return fmt.Errorf("round %d: %s did not report %s", r, bins[side], c)
				}
				v := s.NsPerOp[c]
				v[side] = append(v[side], ns)
				s.NsPerOp[c] = v
			}
		}
		fmt.Fprintf(os.Stderr, "round %d/%d done after %v\n", r+1, *rounds, time.Since(start).Round(time.Second))
	}
	b, err := json.MarshalIndent(s, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, b, 0o644)
}

// runBench runs one test binary and returns ns/op by benchmark name, and
// the cpu line.
func runBench(bin, pattern, benchtime string, cpu int) (map[string]float64, string, error) {
	argv := []string{bin, "-test.run", "^$", "-test.bench", pattern, "-test.benchtime", benchtime,
		"-test.count", "1", "-test.cpu", "1"}
	if cpu >= 0 {
		argv = append([]string{"taskset", "-c", strconv.Itoa(cpu)}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("%s: %w\n%s", strings.Join(argv, " "), err, stdout.Bytes())
	}
	return parseBench(&stdout)
}

// benchLine matches a result line. With -test.cpu 1 the benchmark name has
// no -N suffix.
var benchLine = regexp.MustCompile(`^(Benchmark\S+)\s+\d+\s+([0-9.e+]+) ns/op`)

func parseBench(r io.Reader) (map[string]float64, string, error) {
	res := map[string]float64{}
	var cpuLine string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "cpu: "); ok {
			cpuLine = v
		}
		m := benchLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ns, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			return nil, "", fmt.Errorf("parsing %q: %w", line, err)
		}
		res[m[1]] = ns
	}
	return res, cpuLine, sc.Err()
}

// exactPattern returns the -test.bench pattern selecting exactly the named
// benchmark. -test.bench splits at slashes and matches each level unanchored,
// so each level is quoted and anchored: "K=100" alone also selects K=1000.
func exactPattern(name string) string {
	levels := strings.Split(name, "/")
	for i, l := range levels {
		levels[i] = "^" + regexp.QuoteMeta(l) + "$"
	}
	return strings.Join(levels, "/")
}

func intersect(a, b []string) (both, onlyA, onlyB []string) {
	for _, x := range a {
		if slices.Contains(b, x) {
			both = append(both, x)
		} else {
			onlyA = append(onlyA, x)
		}
	}
	for _, x := range b {
		if !slices.Contains(a, x) {
			onlyB = append(onlyB, x)
		}
	}
	return both, onlyA, onlyB
}

func readSamples(path string) (*Samples, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Samples
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func cmdVerdict(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("verdict", flag.ExitOnError)
	thresholdPct := fs.Float64("threshold", 5, "largest accepted slowdown, in percent")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("verdict takes one samples file")
	}
	s, err := readSamples(fs.Arg(0))
	if err != nil {
		return err
	}
	report, regressed := Report(s, Compare(s, *thresholdPct), *thresholdPct)
	if _, err := io.WriteString(w, report); err != nil {
		return err
	}
	if regressed > 0 {
		return fmt.Errorf("%w: %d case(s)", errRegression, regressed)
	}
	return nil
}

// Report formats the results as the Markdown of the job summary and counts
// the regressed cases.
func Report(s *Samples, results []Result, thresholdPct float64) (md string, regressed int) {
	var w strings.Builder
	fmt.Fprintf(&w, "### Benchmarks: %s (head) vs %s (base)\n\n", s.Head, s.Base)
	fmt.Fprintf(&w, "%d interleaved rounds of %s per case, one core of `%s`. ", s.Rounds, s.Benchtime, s.CPU)
	fmt.Fprintf(&w, "A case fails when its median time ratio exceeds +%.1f%% and the 95%% interval of the median lies above zero.\n\n", thresholdPct)
	fmt.Fprintln(&w, "| case | head/base time | 95% interval | head slower in | verdict |")
	fmt.Fprintln(&w, "|---|---:|---:|---:|---|")
	for _, r := range results {
		fmt.Fprintf(&w, "| `%s` | %+.1f%% | %+.1f%% … %+.1f%% | %d/%d | %s |\n",
			strings.TrimPrefix(r.Name, "BenchmarkGate/"), pct(r.Ratio), pct(r.Lo), pct(r.Hi), r.Slower, r.Rounds, r.Verdict)
		if r.Verdict == Regressed {
			regressed++
		}
	}
	for _, c := range s.OnlyBase {
		fmt.Fprintf(&w, "\nNot compared, missing in the head: `%s`", c)
	}
	for _, c := range s.OnlyHead {
		fmt.Fprintf(&w, "\nNot compared, new in the head: `%s`", c)
	}
	fmt.Fprintln(&w)
	return w.String(), regressed
}

func pct(ratio float64) float64 { return (ratio - 1) * 100 }

func cmdCalibrate(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("calibrate takes one or more samples files")
	}
	var all []*Samples
	for _, p := range fs.Args() {
		s, err := readSamples(p)
		if err != nil {
			return err
		}
		all = append(all, s)
	}
	_, err := io.WriteString(w, CalibrationReport(Calibrate(all, []float64{2, 3, 4, 5, 6, 8, 10, 15})))
	return err
}

// CalibrationReport formats a Calibration as Markdown.
func CalibrationReport(c Calibration) string {
	var w strings.Builder
	fmt.Fprintf(&w, "### Calibration over %d runs\n\nRunner CPUs: %s\n\n", c.Runs, strings.Join(c.CPUs, "; "))
	fmt.Fprintln(&w, "| case | median of medians | largest median | largest interval low end |")
	fmt.Fprintln(&w, "|---|---:|---:|---:|")
	for _, cs := range c.Cases {
		fmt.Fprintf(&w, "| `%s` | %+.1f%% | %+.1f%% | %+.1f%% |\n",
			strings.TrimPrefix(cs.Name, "BenchmarkGate/"), pct(cs.MedianRatio), pct(cs.MaxRatio), pct(cs.MaxLo))
	}
	fmt.Fprintln(&w, "\n| threshold | runs that would fail |")
	fmt.Fprintln(&w, "|---:|---:|")
	for _, t := range c.Thresholds {
		fmt.Fprintf(&w, "| %.0f%% | %d/%d |\n", t.ThresholdPct, t.FailingRuns, c.Runs)
	}
	return w.String()
}
