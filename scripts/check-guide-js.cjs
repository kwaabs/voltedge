const fs = require("fs");
const vm = require("vm");
const path = process.argv[2];
const html = fs.readFileSync(path, "utf8");
const m = html.match(/<script>([\s\S]*?)<\/script>/);
if (!m) { console.log("NO SCRIPT FOUND"); process.exit(2); }
const js = m[1];
try {
  new vm.Script(js); // parse-only; does not execute
  console.log("PARSE OK — script length", js.length);
} catch (e) {
  console.log("PARSE ERROR:", e.message);
  // find approx line
  const stack = String(e.stack).split("\n").slice(0, 4).join("\n");
  console.log(stack);
  process.exit(1);
}
