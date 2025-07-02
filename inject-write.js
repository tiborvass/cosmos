// /usr/bin/env -S node --no-warnings --enable-source-maps --require=/tmp/inject-write.js /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js

const originalWrite = process.stdout.write.bind(process.stdout);

//const fs = require('fs');

process.stdout.write("INJECTED\n");

//const stream = fs.createWriteStream('/tmp/inject.log', { flags: 'a' }); // 'a' to append

const p1 = Buffer.from([
  0x0a, 0x1b, 0x5b, 0x39, 0x35, 0x6d, 0xe2, 0x97, 0x8f, 0x1b, 0x5b, 0x33, 0x39,
  0x6d, 0x20, 0x1b, 0x5b, 0x31, 0x6d,
]);

// TODO: should we add more to the pattern match? (such as: "Error: Bash operation blocked by hook")
const p2 = Buffer.from([0x1b, 0x5b, 0x39, 0x35, 0x6d]);
const p3 = Buffer.from([0x5d, 0x3a, 0x20]);

// The offset of '95' in the pattern buffer (after '\x1b[')
const COLOR_OFFSET_IN_PATTERN = 3;

process.stdout.write = function (chunk, encoding, callback) {
  let buffer = Buffer.isBuffer(chunk)
    ? chunk
    : Buffer.from(chunk, encoding || "utf8");
  let idx = buffer.indexOf(p1);

  if (idx !== -1) {
    let name = "Bash";
    let nameIdx = buffer.indexOf(name, p1.length);
    if (nameIdx !== -1) {
      // Change \x1b[95m (RED) to \x1b[32m (GREEN)
      buffer[idx + COLOR_OFFSET_IN_PATTERN] = 0x33; // '3'
      buffer[idx + COLOR_OFFSET_IN_PATTERN + 1] = 0x32; // '2'
      let leftIdx = buffer.indexOf(p2, nameIdx + name.length);
      if (leftIdx !== -1) {
        idx = buffer.indexOf(p2, leftIdx + p2.length);
        if (idx !== -1) {
          let rightIdx = buffer.indexOf(p3, idx + p2.length);
          if (rightIdx !== -1) {
            originalWrite(buffer.slice(0, leftIdx), encoding, callback);
            return originalWrite(
              buffer.slice(rightIdx + p3.length),
              encoding,
              callback,
            );
          }
        }
      }
    }
  }

  return originalWrite(chunk, encoding, callback);
};
