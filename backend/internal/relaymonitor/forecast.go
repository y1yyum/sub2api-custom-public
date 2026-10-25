package relaymonitor

import (
	"math"
	"sort"
	"time"
)

type ForecastSample struct {
	T    int64   `json:"t"`
	Cost float64 `json:"cost"`
}

func forecastRound(v float64) float64 { return math.Round(v*100) / 100 }
func forecastMean(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	s := 0.
	for _, v := range a {
		s += v
	}
	return s / float64(len(a))
}
func forecastQuantile(a []float64, q float64) float64 {
	if len(a) == 0 {
		return 0
	}
	b := append([]float64{}, a...)
	sort.Float64s(b)
	return b[int(math.Round(q*float64(len(b)-1)))]
}
func forecastMedian(a []float64) float64 {
	b := append([]float64{}, a...)
	sort.Float64s(b)
	n := len(b)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return b[n/2]
	}
	return (b[n/2-1] + b[n/2]) / 2
}
func forecastClamp(v, lo, hi float64) float64 { return math.Min(hi, math.Max(lo, v)) }
func forecastInputs(s []ForecastSample) ([]float64, []int64) {
	a := append([]ForecastSample{}, s...)
	sort.Slice(a, func(i, j int) bool { return a[i].T < a[j].T })
	v := []float64{}
	ts := []int64{}
	for _, p := range a {
		if p.Cost < 0 || math.IsNaN(p.Cost) || math.IsInf(p.Cost, 0) {
			continue
		}
		if len(ts) > 0 && ts[len(ts)-1] == p.T {
			v[len(v)-1] += p.Cost
			continue
		}
		v = append(v, p.Cost)
		ts = append(ts, p.T)
	}
	return v, ts
}
func forecastLogHolt(v []float64, h int) []float64 {
	level, trend := math.Log1p(v[0]), 0.
	for _, x := range v[1:] {
		prev := level
		level = .5*math.Log1p(x) + .5*(level+.9*trend)
		trend = .1*(level-prev) + .9*.9*trend
	}
	out := make([]float64, h)
	acc := 0.
	for k := range out {
		acc += math.Pow(.9, float64(k+1))
		out[k] = math.Max(0, math.Exp(level+acc*trend)-1)
	}
	return out
}
func forecastTheta(v []float64, h int) []float64 {
	n := float64(len(v))
	sx, sy, sxx, sxy := 0., 0., 0., 0.
	level := math.Log1p(v[0])
	for i, x := range v {
		y := math.Log1p(x)
		f := float64(i)
		sx += f
		sy += y
		sxx += f * f
		sxy += f * y
		if i > 0 {
			level = .4*y + .6*level
		}
	}
	b := 0.
	if den := n*sxx - sx*sx; math.Abs(den) > 1e-9 {
		b = (n*sxy - sx*sy) / den
	}
	out := make([]float64, h)
	for k := range out {
		out[k] = math.Max(0, math.Exp(level+.5*b*float64(k+1))-1)
	}
	return out
}
func forecastEns(v []float64, ts []int64, h int) []float64 {
	a, b := forecastLogHolt(v, h), forecastTheta(v, h)
	from := len(v) - 7
	if from < 0 {
		from = 0
	}
	med := forecastMedian(v[from:])
	level := v[0]
	alpha := 1 - math.Pow(.5, .1)
	for _, x := range v[1:] {
		level = alpha*x + (1-alpha)*level
	}
	for k := range a {
		a[k] = (a[k] + b[k] + med + level) / 4
	}
	return a
}
func forecastDow(v []float64, ts []int64, h int, reg bool) []float64 {
	var sum, cnt [7]float64
	for i, x := range v {
		d := int(time.UnixMilli(ts[i]).UTC().Weekday())
		sum[d] += x
		cnt[d]++
	}
	overall := forecastMean(v)
	factors := [7]float64{1, 1, 1, 1, 1, 1, 1}
	if overall > 0 {
		for d := range factors {
			if cnt[d] > 0 {
				ratio := sum[d] / cnt[d] / overall
				if reg {
					factors[d] = forecastClamp(ratio, .3, 3)
				} else {
					factors[d] = forecastClamp(1+(ratio-1)*cnt[d]/(cnt[d]+3), .3, 3)
				}
			}
		}
	}
	out := make([]float64, h)
	if !reg {
		alpha := 1 - math.Pow(.5, .2)
		level := v[0]
		var relative [7][]float64
		for i, x := range v {
			if i > 0 {
				level = alpha*x + (1-alpha)*level
			}
			if level > 0 {
				d := time.UnixMilli(ts[i]).UTC().Weekday()
				relative[d] = append(relative[d], x/level)
			}
		}
		for d, a := range relative {
			factors[d] = 1
			if len(a) > 0 {
				factors[d] = (3 + float64(len(a))*forecastMedian(a)) / (3 + float64(len(a)))
			}
		}

		for k := range out {
			d := time.UnixMilli(ts[len(ts)-1] + int64(k+1)*86400000).UTC().Weekday()
			out[k] = math.Max(0, level*factors[d])
		}
		return out
	}
	sw, sx, sy, sxx, sxy := 0., 0., 0., 0., 0.
	for i, x := range v {
		f := float64(i)
		w := math.Pow(.92, float64(len(v)-1-i))
		y := x / factors[time.UnixMilli(ts[i]).UTC().Weekday()]
		sw += w
		sx += w * f
		sy += w * y
		sxx += w * f * f
		sxy += w * f * y
	}
	b := 0.
	if den := sw*sxx - sx*sx; math.Abs(den) > 1e-9 {
		b = (sw*sxy - sx*sy) / den
	}
	a := (sy - b*sx) / sw
	for k := range out {
		d := time.UnixMilli(ts[len(ts)-1] + int64(k+1)*86400000).UTC().Weekday()
		out[k] = math.Max(0, (a+b*float64(len(v)+k))*factors[d])
	}
	return out
}

type forecastDailyMethod func([]float64, []int64, int) []float64

func forecastBacktest(fn forecastDailyMethod, v []float64, ts []int64) (float64, *float64) {
	e1, a1, e3, a3 := 0., 0., 0., 0.
	start := len(v) - 14
	if start < 5 {
		start = 5
	}
	for i := start; i < len(v); i++ {
		h := 3
		if len(v)-i < h {
			h = len(v) - i
		}
		p := fn(v[:i], ts[:i], h)
		e1 += math.Abs(p[0] - v[i])
		a1 += v[i]
		if h == 3 {
			pred, actual := 0., 0.
			for k := 0; k < h; k++ {
				pred += p[k]
				actual += v[i+k]
			}
			e3 += math.Abs(pred - actual)
			a3 += actual
		}
	}
	s1, s3 := 9., 9.
	var wape *float64
	if a1 > 0 {
		x := e1 / a1
		s1 = x
		wape = &x
	}
	if a3 > 0 {
		s3 = e3 / a3
	}
	return s1 + s3, wape
}
func ForecastDaily(samples []ForecastSample, days int) map[string]any {
	v, ts := forecastInputs(samples)
	n := len(v)
	if n < 3 || days < 1 || days > 366 {
		return nil
	}
	fn := forecastDailyMethod(forecastEns)
	label := "四模型组合"
	var wape *float64
	if n >= 8 {
		var score float64
		score, wape = forecastBacktest(fn, v, ts)
		if n >= 14 {
			for _, c := range []struct {
				label string
				fn    forecastDailyMethod
			}{{"加权回归 + 星期因子", func(v []float64, t []int64, h int) []float64 { return forecastDow(v, t, h, true) }}, {"均线 + 星期因子", func(v []float64, t []int64, h int) []float64 { return forecastDow(v, t, h, false) }}} {
				s, w := forecastBacktest(c.fn, v, ts)
				if s < score*.8 {
					fn = c.fn
					label = c.label
					wape = w
					break
				}
			}
		}
	}
	pred := fn(v, ts, days)
	byK := make([][]float64, days)
	all, tot := []float64{}, []float64{}
	if n >= 8 {
		start := n - 28
		if start < 5 {
			start = 5
		}
		for i := start; i < n; i++ {
			h := days
			if n-i < h {
				h = n - i
			}
			p := fn(v[:i], ts[:i], h)
			ps, actual := 0., 0.
			for k := 0; k < h; k++ {
				if p[k] > .01 {
					ratio := v[i+k] / p[k]
					byK[k] = append(byK[k], ratio)
					all = append(all, ratio)
				}
				ps += p[k]
				actual += v[i+k]
			}
			if h == days && ps > .01 {
				tot = append(tot, actual/ps)
			}
		}
	}
	points := []map[string]any{}
	total := 0.
	for k, p := range pred {
		src := byK[k]
		if len(src) < 8 {
			src = all
		}
		lo, hi := 0., 0.
		if len(src) >= 5 {
			lo = p * forecastClamp(forecastQuantile(src, .1), .05, 1)
			hi = p * forecastClamp(forecastQuantile(src, .9), 1, 5)
		} else {
			widen := math.Min(math.Sqrt(float64(k+1)), 1.8)
			lo = math.Max(0, p*(1-.4*widen))
			hi = p * (1 + .4*widen)
		}
		cost := forecastRound(p)
		total += cost
		points = append(points, map[string]any{"t": ts[n-1] + int64(k+1)*86400000, "cost": cost, "lo": forecastRound(lo), "hi": forecastRound(hi)})
	}
	total = forecastRound(total)
	lo, hi := .6, 1.4
	if len(tot) >= 5 {
		lo = forecastClamp(forecastQuantile(tot, .1), .2, 1)
		hi = forecastClamp(forecastQuantile(tot, .9), 1, 3)
	}
	var back any
	if wape != nil {
		back = math.Round(*wape * 100)
	}
	return map[string]any{"points": points, "nextTotal": total, "nextLo": forecastRound(total * lo), "nextHi": forecastRound(total * hi), "method": label, "sampleDays": n, "backtestWapePct": back}
}
func forecastHourlyPredict(v []float64, ts []int64, end, horizon int) []float64 {
	last := ts[end-1]
	var sum, weights [24]float64
	for i := 0; i < end; i++ {
		if ts[i] < last-14*86400000 {
			continue
		}
		w := math.Pow(.5, float64(last-ts[i])/86400000/3)
		hour := time.UnixMilli(ts[i]).UTC().Hour()
		weights[hour] += w
		sum[hour] += w * v[i]
	}
	profileSum := 0.
	for h := range sum {
		if weights[h] > 0 {
			sum[h] /= weights[h]
		}
		profileSum += sum[h]
	}
	totals := []float64{}
	for d := 1; d <= 5 && d*24 <= end; d++ {
		total := 0.
		for i := end - d*24; i < end-(d-1)*24; i++ {
			total += v[i]
		}
		totals = append(totals, total)
	}
	total := profileSum
	if len(totals) > 0 {
		total = forecastMedian(totals)
	}
	out := make([]float64, horizon)
	for k := range out {
		h := time.UnixMilli(last + int64(k+1)*3600000).UTC().Hour()
		if profileSum > 1e-9 {
			out[k] = math.Max(0, sum[h]/profileSum*total)
		}
	}
	return out
}
func ForecastHourly(samples []ForecastSample, hours int) map[string]any {
	v, ts := forecastInputs(samples)
	n := len(v)
	if n < 72 || hours < 1 || hours > 24*31 {
		return nil
	}
	pred := forecastHourlyPredict(v, ts, n, hours)
	ratios, adds := []float64{}, []float64{}
	esum, asum := 0., 0.
	start := n - 14*24
	if start < 120 {
		start = 120
	}
	for o := start; o+24 <= n; o += 12 {
		p := forecastHourlyPredict(v, ts, o, 24)
		ps, actual := 0., 0.
		for k := 0; k < 24; k++ {
			a := v[o+k]
			ps += p[k]
			actual += a
			if p[k] > .5 {
				ratios = append(ratios, a/p[k])
			} else {
				adds = append(adds, a-p[k])
			}
		}
		esum += math.Abs(ps - actual)
		asum += actual
	}
	rlo, rhi, alo, ahi := .3, 2.5, -.5, .5
	if len(ratios) >= 20 {
		rlo = forecastClamp(forecastQuantile(ratios, .2), .05, 1)
		rhi = forecastClamp(forecastQuantile(ratios, .8), 1, 6)
	}
	if len(adds) >= 20 {
		alo = math.Min(0, forecastQuantile(adds, .2))
		ahi = math.Max(0, forecastQuantile(adds, .8))
	}
	points := []map[string]any{}
	total := 0.
	for k, p := range pred {
		lo, hi := p+alo, p+ahi
		if p > .5 {
			lo, hi = p*rlo, p*rhi
		}
		cost := forecastRound(p)
		if k < 24 {
			total += cost
		}
		points = append(points, map[string]any{"t": ts[n-1] + int64(k+1)*3600000, "cost": cost, "lo": forecastRound(math.Max(0, lo)), "hi": forecastRound(hi)})
	}
	var back any
	if asum > 0 {
		back = math.Round(esum / asum * 100)
	}
	return map[string]any{"points": points, "next24Total": forecastRound(total), "sampleHours": n, "backtestWapePct": back}
}
func PredictExhaustion(snapshots []Snapshot) map[string]any {
	type point struct {
		t time.Time
		r float64
	}
	pts := []point{}
	for _, s := range snapshots {
		if !s.OK {
			continue
		}
		remaining := s.RemainingCNY
		if remaining == nil {
			remaining = s.Remaining
		}
		if remaining == nil {
			continue
		}
		d, e := providerDecimal(*remaining)
		if e != nil {
			continue
		}
		v, _ := d.Float64()
		pts = append(pts, point{s.CheckedAt, v})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].t.Before(pts[j].t) })
	if len(pts) < 3 {
		return nil
	}
	latest := pts[len(pts)-1]
	burn, span := 0., 0.
	samples := 0
	basis := ""
	for _, w := range []struct {
		hours, minSpan float64
		label          string
	}{{3, 1, "近3小时"}, {12, 2, "近12小时"}} {
		seg := []point{}
		cut := time.Now().Add(-time.Duration(w.hours) * time.Hour)
		for _, p := range pts {
			if !p.t.Before(cut) {
				seg = append(seg, p)
			}
		}
		if len(seg) < 5 {
			continue
		}
		h := seg[len(seg)-1].t.Sub(seg[0].t).Hours()
		if h < w.minSpan {
			continue
		}
		drop := 0.
		for i := 1; i < len(seg); i++ {
			drop += math.Max(0, seg[i-1].r-seg[i].r)
		}
		burn = drop / h * 24
		span = h
		samples = len(seg)
		basis = w.label
		break
	}
	if basis == "" {
		seg := []point{}
		cut := time.Now().Add(-48 * time.Hour)
		for _, p := range pts {
			if !p.t.Before(cut) {
				seg = append(seg, p)
			}
		}
		if len(seg) < 3 {
			from := len(pts) - 50
			if from < 0 {
				from = 0
			}
			seg = pts[from:]
		}
		start := 0
		for i := 1; i < len(seg); i++ {
			if seg[i].r > seg[i-1].r+.05 {
				start = i
			}
		}
		seg = seg[start:]
		if len(seg) < 3 {
			return nil
		}
		span = seg[len(seg)-1].t.Sub(seg[0].t).Hours()
		if span < 1./6 {
			return nil
		}
		sx, sy, sxx, sxy := 0., 0., 0., 0.
		for _, p := range seg {
			x := p.t.Sub(seg[0].t).Hours()
			sx += x
			sy += p.r
			sxx += x * x
			sxy += x * p.r
		}
		n := float64(len(seg))
		den := n*sxx - sx*sx
		if math.Abs(den) < 1e-9 {
			return nil
		}
		slope := (n*sxy - sx*sy) / den
		burn = math.Max(0, -slope*24)
		samples = len(seg)
		basis = "回归"
	}
	result := map[string]any{"burnPerDay": forecastRound(burn), "etaDays": nil, "etaAt": nil, "basis": basis, "samples": samples, "spanHours": math.Round(span*10) / 10}
	if burn >= .0001 {
		days := math.Max(0, latest.r) / burn
		result["etaDays"] = math.Round(days*10) / 10
		if days < 36500 {
			result["etaAt"] = latest.t.Add(time.Duration(days * 24 * float64(time.Hour))).UTC().Format(time.RFC3339)
		}
	}
	return result
}
