/* Tiny inline-SVG bar chart for the explorer stats view. No dependencies.
 * Single-series bars, rounded data ends anchored to the baseline, a 2px gap
 * between bars, recessive grid, optional dashed reference line, and a hover
 * tooltip per bar (plus <title> for assistive tech). */
(function () {
  "use strict";
  var esc = function (s) { return window.QSDMX.fmt.esc(s); };

  function niceMax(v) {
    if (!(v > 0)) return { max: 1, ticks: 4 };
    var p = Math.pow(10, Math.floor(Math.log10(v)));
    var n = v / p;
    var m = n <= 1 ? 1 : n <= 2 ? 2 : n <= 2.5 ? 2.5 : n <= 5 ? 5 : 10;
    return { max: m * p, ticks: (m === 2.5 || m === 5 || m === 10) ? 5 : 4 };
  }

  function roundedTop(x, y, w, h, r) {
    if (h <= 0) return "";
    r = Math.min(r, w / 2, h);
    return "M" + x + "," + (y + h) +
      "V" + (y + r) +
      "Q" + x + "," + y + " " + (x + r) + "," + y +
      "H" + (x + w - r) +
      "Q" + (x + w) + "," + y + " " + (x + w) + "," + (y + r) +
      "V" + (y + h) + "Z";
  }

  /**
   * bars(opts) -> SVG string
   *  data:   [{label, value, tip}]
   *  color:  CSS colour for the bars
   *  yFmt:   value -> axis label
   *  xEvery: show every Nth x label
   *  ref:    {value, label} dashed reference line (in value units) - optional
   *  refX:   {index, label} dashed vertical reference at a bar index - optional
   *  title:  accessible name
   */
  function bars(opts) {
    var data = opts.data || [];
    var W = opts.width || 1000, H = opts.height || 260;
    var m = { t: 24, r: 12, b: 30, l: 46 };
    var iw = W - m.l - m.r, ih = H - m.t - m.b;
    var nm = niceMax(Math.max.apply(null, data.map(function (d) { return d.value; }).concat([opts.ref ? opts.ref.value : 0, 0])));
    var max = nm.max, ticks = nm.ticks;
    var n = Math.max(data.length, 1);
    var step = iw / n;
    var gap = Math.min(2, step * 0.3);
    var bw = Math.max(step - gap, 1);
    var yFmt = opts.yFmt || function (v) { return String(v); };
    var out = [];
    out.push('<svg class="chart" viewBox="0 0 ' + W + " " + H + '" role="img" aria-label="' + esc(opts.title || "chart") + '" preserveAspectRatio="xMidYMid meet">');
    // grid + y labels
    for (var i = 0; i <= ticks; i++) {
      var v = max * i / ticks;
      var y = m.t + ih - ih * i / ticks;
      out.push('<line class="grid" x1="' + m.l + '" x2="' + (W - m.r) + '" y1="' + y + '" y2="' + y + '"/>');
      out.push('<text class="ylab" x="' + (m.l - 6) + '" y="' + (y + 4) + '" text-anchor="end">' + esc(yFmt(v)) + "</text>");
    }
    // bars
    data.forEach(function (d, idx) {
      var h = max > 0 ? ih * d.value / max : 0;
      var x = m.l + idx * step + gap / 2;
      var y = m.t + ih - h;
      // invisible hit target spanning the full column
      out.push('<g class="bar" data-tip="' + esc(d.tip || (d.label + ": " + d.value)) + '">' +
        "<title>" + esc(d.tip || (d.label + ": " + d.value)) + "</title>" +
        '<rect class="hit" x="' + (m.l + idx * step) + '" y="' + m.t + '" width="' + step + '" height="' + ih + '"/>' +
        (h > 0 ? '<path d="' + roundedTop(x, y, bw, h, Math.min(4, bw / 2)) + '" fill="' + esc(opts.color || "currentColor") + '"/>' : "") +
        "</g>");
      var every = opts.xEvery || Math.ceil(n / 8);
      if (idx % every === 0) {
        out.push('<text class="xlab" x="' + (m.l + idx * step + step / 2) + '" y="' + (H - 10) + '" text-anchor="middle">' + esc(d.label) + "</text>");
      }
    });
    // reference lines
    if (opts.ref && max > 0) {
      var ry = m.t + ih - ih * opts.ref.value / max;
      out.push('<line class="ref" x1="' + m.l + '" x2="' + (W - m.r) + '" y1="' + ry + '" y2="' + ry + '"/>');
      out.push('<text class="reflab" x="' + (W - m.r) + '" y="' + (ry - 5) + '" text-anchor="end">' + esc(opts.ref.label) + "</text>");
    }
    if (opts.refX) {
      var rx = m.l + opts.refX.index * step + step / 2;
      out.push('<line class="ref" x1="' + rx + '" x2="' + rx + '" y1="' + m.t + '" y2="' + (m.t + ih) + '"/>');
      out.push('<text class="reflab" x="' + rx + '" y="' + (m.t - 8) + '" text-anchor="middle">' + esc(opts.refX.label) + "</text>");
    }
    out.push('<line class="axis" x1="' + m.l + '" x2="' + (W - m.r) + '" y1="' + (m.t + ih) + '" y2="' + (m.t + ih) + '"/>');
    out.push("</svg>");
    return out.join("");
  }

  // One floating tooltip for every chart on the page.
  var tip = null;
  function ensureTip() {
    if (tip) return tip;
    tip = document.createElement("div");
    tip.className = "chart-tip";
    tip.hidden = true;
    document.body.appendChild(tip);
    return tip;
  }
  document.addEventListener("mousemove", function (ev) {
    var g = ev.target.closest && ev.target.closest("svg.chart g.bar[data-tip]");
    var t = ensureTip();
    if (!g) { t.hidden = true; return; }
    t.textContent = g.getAttribute("data-tip");
    t.hidden = false;
    var x = Math.min(ev.clientX + 14, window.innerWidth - t.offsetWidth - 8);
    t.style.left = x + "px";
    t.style.top = (ev.clientY + 14) + "px";
  });

  window.QSDMCharts = { bars: bars };
})();
