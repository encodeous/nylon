package state

import (
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"math"
	"math/rand/v2"
	"net/netip"
	"testing"
	"time"
)

import (
	"image/color"
)

type DataSource struct {
	Name string
	Data []time.Duration
}

func generateMultiLinePlot(dataSources []DataSource, title string) (*plot.Plot, error) {
	p := plot.New()

	p.Title.Text = title
	p.X.Label.Text = "Sample #"
	p.Y.Label.Text = "Duration (ms)"

	// Define a color palette for the lines
	colors := []color.Color{
		color.RGBA{R: 255, G: 0, B: 0, A: 255},   // Red
		color.RGBA{R: 0, G: 0, B: 255, A: 255},   // Blue
		color.RGBA{R: 0, G: 255, B: 0, A: 255},   // Green
		color.RGBA{R: 255, G: 0, B: 255, A: 255}, // Magenta
		color.RGBA{R: 0, G: 255, B: 255, A: 255}, // Cyan
	}

	for i, ds := range dataSources {
		points := make(plotter.XYs, len(ds.Data))
		for j, d := range ds.Data {
			points[j].X = float64(j)
			points[j].Y = float64(d.Milliseconds())
		}

		line, err := plotter.NewLine(points)
		if err != nil {
			return nil, fmt.Errorf("failed to create line for %s: %v", ds.Name, err)
		}

		line.Color = colors[i%len(colors)] // Cycle through colors
		p.Add(line)
		p.Legend.Add(ds.Name, line)
	}

	return p, nil
}

func runTests(t *testing.T, ping func(i int) float64, dura time.Duration, fn string) (DataSource, DataSource) {
	t.Helper()
	dep := NewEndpoint(netip.AddrPort{}, "dummy", false, nil)

	truth := DataSource{
		Name: "Truth",
		Data: []time.Duration{},
	}

	low := DataSource{
		Name: "Low",
		Data: []time.Duration{},
	}

	high := DataSource{
		Name: "High",
		Data: []time.Duration{},
	}

	filtered := DataSource{
		Name: "Filtered",
		Data: []time.Duration{},
	}

	stabilized := DataSource{
		Name: "Stabilized",
		Data: []time.Duration{},
	}

	samples := int(dura / ProbeDelay)
	for i := 0; i < samples; i++ {
		nping := time.Duration(ping(i) * float64(time.Millisecond))
		dep.UpdatePing(nping)
		if i > MinimumConfidenceWindow {
			truth.Data = append(truth.Data, nping)
			high.Data = append(high.Data, dep.HighRange())
			low.Data = append(low.Data, dep.LowRange())
			filtered.Data = append(filtered.Data, dep.FilteredPing())
			stabilized.Data = append(stabilized.Data, dep.StabilizedPing())
		}
	}

	dataSources := []DataSource{truth, high, low, filtered, stabilized}

	p, err := generateMultiLinePlot(dataSources, "Comparison of ping and stabilized ping")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save(8*vg.Inch, 6*vg.Inch, spew.Sprintf("method_comparison_%s.png", fn)); err != nil {
		t.Fatalf("Failed to save plot: %v", err)
	}

	return truth, stabilized
}

func TestEndpointSin(t *testing.T) {
	rng := rand.New(rand.NewPCG(0, 0))
	truth, finalFiltered := runTests(t, func(i int) float64 {
		val := math.Cos(float64(i)/1000.0-math.Pi/2) * 10
		if rng.Int()%30 == 0 {
			val += float64(rng.Int() % 20)
		}
		val2 := math.Sin(float64(i+400)/50.0)*2 + rng.Float64()
		val3 := math.Abs(rng.NormFloat64()) * 5
		return val + val2 + val3 + 75
	}, time.Hour*2, "sin")

	distinctValues := make(map[uint64]struct{})

	variance := 0.0
	for i, d := range finalFiltered.Data {
		distinctValues[uint64(d)] = struct{}{}
		diff := float64(d - truth.Data[i])
		variance += diff * diff
	}
	// deviation from pingY should be 10 + 5 + 2 = 17ms
	stdev := math.Sqrt(variance / float64(len(finalFiltered.Data)))
	assert.Less(t, time.Duration(stdev), time.Millisecond*20)
	assert.Less(t, len(distinctValues), int((time.Hour*2)/time.Minute))
}

func TestEndpointPosX(t *testing.T) {
	// absolute worst case scenario for number of metric changes
	rng := rand.New(rand.NewPCG(0, 0))
	truth, finalFiltered := runTests(t, func(i int) float64 {
		val := float64(i) / 50.0
		if rng.Int()%30 == 0 {
			val += float64(rng.Int() % 20)
		}
		val2 := math.Sin(float64(i+400)/50.0)*2 + rng.Float64()
		val3 := math.Abs(rng.NormFloat64()) * 5
		return val + val2 + val3 + 75
	}, time.Hour*2, "PosX")

	distinctValues := make(map[uint64]struct{})

	variance := 0.0
	for i, d := range finalFiltered.Data {
		distinctValues[uint64(d)] = struct{}{}
		diff := float64(d - truth.Data[i])
		variance += diff * diff
	}
	stdev := math.Sqrt(variance / float64(len(finalFiltered.Data)))
	assert.Less(t, time.Duration(stdev), time.Millisecond*20)
	// once per minute is acceptable
	assert.Less(t, len(distinctValues), int(time.Hour*2/time.Minute))
}

func TestEndpointNegX(t *testing.T) {
	// absolute worst case scenario for number of metric changes
	rng := rand.New(rand.NewPCG(0, 0))
	truth, finalFiltered := runTests(t, func(i int) float64 {
		val := -float64(i) / 50.0
		if rng.Int()%30 == 0 {
			val += float64(rng.Int() % 20)
		}
		val2 := math.Sin(float64(i+400)/50.0)*2 + rng.Float64()
		val3 := math.Abs(rng.NormFloat64()) * 5
		return val + val2 + val3 + 500
	}, time.Hour*2, "NegX")

	distinctValues := make(map[uint64]struct{})

	variance := 0.0
	for i, d := range finalFiltered.Data {
		distinctValues[uint64(d)] = struct{}{}
		diff := float64(d - truth.Data[i])
		variance += diff * diff
	}
	stdev := math.Sqrt(variance / float64(len(finalFiltered.Data)))
	assert.Less(t, time.Duration(stdev), time.Millisecond*40)
	// once per minute is acceptable
	assert.Less(t, len(distinctValues), int(time.Hour*2/time.Minute))
}

func TestEndpointNormal(t *testing.T) {
	// absolute worst case scenario for number of metric changes
	rng := rand.New(rand.NewPCG(0, 0))
	truth, finalFiltered := runTests(t, func(i int) float64 {
		return 50 + rng.NormFloat64()*10
	}, time.Hour*2, "normal")

	distinctValues := make(map[uint64]struct{})

	variance := 0.0
	for i, d := range finalFiltered.Data {
		distinctValues[uint64(d)] = struct{}{}
		diff := float64(d - truth.Data[i])
		variance += diff * diff
	}
	stdev := math.Sqrt(variance / float64(len(finalFiltered.Data)))
	assert.Less(t, time.Duration(stdev), time.Millisecond*40)
	// once per minute is acceptable
	assert.Less(t, len(distinctValues), int(time.Hour*2/time.Minute))
}
