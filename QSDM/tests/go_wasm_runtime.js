"use strict";

class Go {
  constructor() {
    this.argv = [];
    this.env = {};
    this.exit = (code) => {
      if (code !== 0) {
        throw new Error("exit code: " + code);
      }
    };
    this._pendingEvent = null;
    this._scheduledTimeouts = new Map();
    this._nextCallbackTimeoutID = 1;
    this.importObject = {
      go: {
        "runtime.wasmExit": (sp) => {
          const code = this.mem.getInt32(sp + 4, true);
          this.exit(code);
        },
        "runtime.wasmWrite": (sp) => {
          const fd = this.mem.getInt32(sp + 4, true);
          const p = this.mem.getInt32(sp + 8, true);
          const n = this.mem.getInt32(sp + 12, true);
          const bytes = new Uint8Array(this.mem.buffer, p, n);
          const str = new TextDecoder("utf-8").decode(bytes);
          if (fd === 1) {
            process.stdout.write(str);
          } else if (fd === 2) {
            process.stderr.write(str);
          }
          return n;
        },
        // Add other imports as needed...
      }
    };
  }

  async run(instance) {
    this.mem = new DataView(instance.exports.mem.buffer);
    if (instance.exports.run) {
      instance.exports.run();
    } else if (instance.exports._start) {
      instance.exports._start();
    } else {
      throw new Error("No entry point found in WASM module");
    }
  }
}

module.exports = { Go };
