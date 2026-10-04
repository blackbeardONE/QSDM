/* Minimal QR code encoder (byte mode, error correction level M, versions 1-10).
 * Written for the QSDM web wallet "Receive" view so the address QR is drawn
 * locally: no CDN, no third-party QR service, no network request.
 * Follows ISO/IEC 18004 (structure modelled on Project Nayuki's reference
 * description). Released under the MIT license; see LICENSE-qr.txt.
 *
 *   QSDMQR.matrix(text)        -> { size, modules: boolean[][] }
 *   QSDMQR.svg(text, {scale})  -> SVG markup string (dark on white, 4-module quiet zone)
 */
(function () {
  "use strict";

  // [total codewords, ec codewords per block, [[blocks, data codewords], ...]] for level M
  var TABLE_M = [
    null,
    [26, 10, [[1, 16]]],
    [44, 16, [[1, 28]]],
    [70, 26, [[1, 44]]],
    [100, 18, [[2, 32]]],
    [134, 24, [[2, 43]]],
    [172, 16, [[4, 27]]],
    [196, 18, [[4, 31]]],
    [242, 22, [[2, 38], [2, 39]]],
    [292, 22, [[3, 36], [2, 37]]],
    [346, 26, [[4, 43], [1, 44]]]
  ];
  var ALIGN = [null, [], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34], [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50]];

  function dataCapacity(ver) {
    return TABLE_M[ver][2].reduce(function (s, g) { return s + g[0] * g[1]; }, 0);
  }

  function utf8(text) {
    var out = [];
    var s = unescape(encodeURIComponent(String(text)));
    for (var i = 0; i < s.length; i++) out.push(s.charCodeAt(i));
    return out;
  }

  // ---------------------------------------------------------- Reed-Solomon GF(256)
  function gfMul(x, y) {
    var z = 0;
    for (var i = 7; i >= 0; i--) {
      z = (z << 1) ^ ((z >>> 7) * 0x11D);
      z ^= ((y >>> i) & 1) * x;
    }
    return z & 0xFF;
  }

  function rsDivisor(degree) {
    var result = new Array(degree).fill(0);
    result[degree - 1] = 1;
    var root = 1;
    for (var i = 0; i < degree; i++) {
      for (var j = 0; j < result.length; j++) {
        result[j] = gfMul(result[j], root);
        if (j + 1 < result.length) result[j] ^= result[j + 1];
      }
      root = gfMul(root, 0x02);
    }
    return result;
  }

  function rsRemainder(data, divisor) {
    var result = new Array(divisor.length).fill(0);
    data.forEach(function (b) {
      var factor = b ^ result.shift();
      result.push(0);
      for (var i = 0; i < divisor.length; i++) result[i] ^= gfMul(divisor[i], factor);
    });
    return result;
  }

  // ---------------------------------------------------------- codewords
  function buildCodewords(bytes, ver) {
    var bits = [];
    function push(val, len) { for (var i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1); }
    push(0x4, 4);                                  // byte mode
    push(bytes.length, ver < 10 ? 8 : 16);         // character count
    bytes.forEach(function (b) { push(b, 8); });
    var capBits = dataCapacity(ver) * 8;
    push(0, Math.min(4, capBits - bits.length));   // terminator
    while (bits.length % 8) bits.push(0);
    var data = [];
    for (var i = 0; i < bits.length; i += 8) {
      var v = 0;
      for (var j = 0; j < 8; j++) v = (v << 1) | bits[i + j];
      data.push(v);
    }
    for (var pad = 0xEC; data.length < dataCapacity(ver); pad ^= 0xEC ^ 0x11) data.push(pad);

    // split into blocks, add EC, interleave
    var ecLen = TABLE_M[ver][1];
    var div = rsDivisor(ecLen);
    var blocks = [];
    var k = 0;
    TABLE_M[ver][2].forEach(function (g) {
      for (var n = 0; n < g[0]; n++) {
        var d = data.slice(k, k + g[1]);
        k += g[1];
        blocks.push({ data: d, ec: rsRemainder(d, div) });
      }
    });
    var out = [];
    var maxData = Math.max.apply(null, blocks.map(function (b) { return b.data.length; }));
    for (var i2 = 0; i2 < maxData; i2++) blocks.forEach(function (b) { if (i2 < b.data.length) out.push(b.data[i2]); });
    for (var e = 0; e < ecLen; e++) blocks.forEach(function (b) { out.push(b.ec[e]); });
    return out;
  }

  // ---------------------------------------------------------- matrix
  function Matrix(ver) {
    this.ver = ver;
    this.size = ver * 4 + 17;
    this.mod = [];
    this.fn = [];
    for (var y = 0; y < this.size; y++) {
      this.mod.push(new Array(this.size).fill(false));
      this.fn.push(new Array(this.size).fill(false));
    }
  }
  Matrix.prototype.setFn = function (x, y, dark) { this.mod[y][x] = !!dark; this.fn[y][x] = true; };

  Matrix.prototype.drawFunctionPatterns = function () {
    var s = this.size, i;
    for (i = 0; i < s; i++) { this.setFn(6, i, i % 2 === 0); this.setFn(i, 6, i % 2 === 0); }
    this.finder(3, 3); this.finder(s - 4, 3); this.finder(3, s - 4);
    var al = ALIGN[this.ver], n = al.length;
    for (i = 0; i < n; i++) {
      for (var j = 0; j < n; j++) {
        if ((i === 0 && j === 0) || (i === 0 && j === n - 1) || (i === n - 1 && j === 0)) continue;
        this.align(al[i], al[j]);
      }
    }
    this.drawFormat(0); // reserve; real bits drawn after masking
    this.drawVersion();
  };
  Matrix.prototype.finder = function (cx, cy) {
    for (var dy = -4; dy <= 4; dy++) {
      for (var dx = -4; dx <= 4; dx++) {
        var d = Math.max(Math.abs(dx), Math.abs(dy)), x = cx + dx, y = cy + dy;
        if (x >= 0 && x < this.size && y >= 0 && y < this.size) this.setFn(x, y, d !== 2 && d !== 4);
      }
    }
  };
  Matrix.prototype.align = function (cx, cy) {
    for (var dy = -2; dy <= 2; dy++) {
      for (var dx = -2; dx <= 2; dx++) this.setFn(cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
    }
  };
  Matrix.prototype.drawFormat = function (mask) {
    var data = (0 << 3) | mask; // level M = 0b00
    var rem = data;
    for (var i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    var bits = ((data << 10) | rem) ^ 0x5412;
    var bit = function (k) { return ((bits >>> k) & 1) !== 0; };
    var s = this.size;
    for (i = 0; i <= 5; i++) this.setFn(8, i, bit(i));
    this.setFn(8, 7, bit(6)); this.setFn(8, 8, bit(7)); this.setFn(7, 8, bit(8));
    for (i = 9; i < 15; i++) this.setFn(14 - i, 8, bit(i));
    for (i = 0; i < 8; i++) this.setFn(s - 1 - i, 8, bit(i));
    for (i = 8; i < 15; i++) this.setFn(8, s - 15 + i, bit(i));
    this.setFn(8, s - 8, true);
  };
  Matrix.prototype.drawVersion = function () {
    if (this.ver < 7) return;
    var rem = this.ver;
    for (var i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1F25);
    var bits = (this.ver << 12) | rem;
    for (i = 0; i < 18; i++) {
      var dark = ((bits >>> i) & 1) !== 0;
      var a = this.size - 11 + (i % 3), b = Math.floor(i / 3);
      this.setFn(a, b, dark); this.setFn(b, a, dark);
    }
  };
  Matrix.prototype.drawCodewords = function (cw) {
    var s = this.size, i = 0, total = cw.length * 8;
    for (var right = s - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5;
      for (var vert = 0; vert < s; vert++) {
        for (var j = 0; j < 2; j++) {
          var x = right - j;
          var upward = ((right + 1) & 2) === 0;
          var y = upward ? s - 1 - vert : vert;
          if (!this.fn[y][x] && i < total) {
            this.mod[y][x] = ((cw[i >>> 3] >>> (7 - (i & 7))) & 1) !== 0;
            i++;
          }
        }
      }
    }
  };
  function maskBit(m, x, y) {
    switch (m) {
      case 0: return (x + y) % 2 === 0;
      case 1: return y % 2 === 0;
      case 2: return x % 3 === 0;
      case 3: return (x + y) % 3 === 0;
      case 4: return (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0;
      case 5: return (x * y) % 2 + (x * y) % 3 === 0;
      case 6: return ((x * y) % 2 + (x * y) % 3) % 2 === 0;
      default: return ((x + y) % 2 + (x * y) % 3) % 2 === 0;
    }
  }
  Matrix.prototype.applyMask = function (m) {
    for (var y = 0; y < this.size; y++) {
      for (var x = 0; x < this.size; x++) {
        if (!this.fn[y][x] && maskBit(m, x, y)) this.mod[y][x] = !this.mod[y][x];
      }
    }
  };
  // Penalty: runs (N1), 2x2 blocks (N2), finder-like patterns (N3), balance (N4).
  Matrix.prototype.penalty = function () {
    var s = this.size, m = this.mod, p = 0, x, y;
    function lines(get) {
      for (var a = 0; a < s; a++) {
        var run = 1, line = [];
        for (var b = 0; b < s; b++) {
          var v = get(a, b);
          line.push(v ? 1 : 0);
          if (b > 0) {
            if (v === get(a, b - 1)) { run++; if (run === 5) p += 3; else if (run > 5) p++; }
            else run = 1;
          }
        }
        var str = line.join("");
        var re = /(?=(10111010000|00001011101))/g;
        while (re.exec(str)) { p += 40; re.lastIndex++; }
      }
    }
    lines(function (a, b) { return m[a][b]; });
    lines(function (a, b) { return m[b][a]; });
    for (y = 0; y < s - 1; y++) {
      for (x = 0; x < s - 1; x++) {
        var c = m[y][x];
        if (c === m[y][x + 1] && c === m[y + 1][x] && c === m[y + 1][x + 1]) p += 3;
      }
    }
    var dark = 0;
    for (y = 0; y < s; y++) for (x = 0; x < s; x++) if (m[y][x]) dark++;
    var k = Math.ceil(Math.abs(dark * 20 - s * s * 10) / (s * s)) - 1;
    p += Math.max(0, k) * 10;
    return p;
  };

  function matrix(text) {
    var bytes = utf8(text);
    var ver = 0;
    for (var v = 1; v <= 10; v++) {
      if (4 + (v < 10 ? 8 : 16) + bytes.length * 8 <= dataCapacity(v) * 8) { ver = v; break; }
    }
    if (!ver) throw new Error("QR: text too long for this encoder (max ~213 bytes)");
    var cw = buildCodewords(bytes, ver);
    var best = null, bestPenalty = Infinity;
    // Masks 1 and 2 (pure stripes) are valid but trip some camera decoders,
    // so they are skipped; the penalty score picks among the rest.
    var masks = (typeof globalThis !== "undefined" && typeof globalThis.__QR_MASK === "number") ? [globalThis.__QR_MASK] : [0, 3, 4, 5, 6, 7];
    for (var mi = 0; mi < masks.length; mi++) {
      var mask = masks[mi];
      var mx = new Matrix(ver);
      mx.drawFunctionPatterns();
      mx.drawCodewords(cw);
      mx.applyMask(mask);
      mx.drawFormat(mask);
      var pen = mx.penalty();
      if (pen < bestPenalty) { bestPenalty = pen; best = mx; }
    }
    return { size: best.size, version: ver, modules: best.mod };
  }

  function svg(text, opts) {
    opts = opts || {};
    var q = matrix(text);
    var border = 4, n = q.size + border * 2;
    var d = [];
    for (var y = 0; y < q.size; y++) {
      for (var x = 0; x < q.size; x++) {
        if (q.modules[y][x]) d.push("M" + (x + border) + "," + (y + border) + "h1v1h-1z");
      }
    }
    var px = (opts.scale || 5) * n;
    return '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ' + n + " " + n + '" width="' + px + '" height="' + px +
      '" shape-rendering="crispEdges" role="img" aria-label="' + (opts.label || "QR code").replace(/"/g, "&quot;") + '">' +
      '<rect width="100%" height="100%" fill="#ffffff"/><path d="' + d.join("") + '" fill="#061116"/></svg>';
  }

  var api = { matrix: matrix, svg: svg };
  if (typeof window !== "undefined") window.QSDMQR = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})();
