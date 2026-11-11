import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, readdirSync, rmSync } from "node:fs";
import path from "node:path";

const [executable, expected] = process.argv.slice(2);
assert(executable && expected, "Usage: test-mcp.mjs <executable> <comma-separated-tools>");
const binary = path.resolve(executable);
const temporary = mkdtempSync(path.resolve("build/mcp-inspector-"));

async function inspect(method) {
  const args = [
    "--yes", "@modelcontextprotocol/inspector@2.14.0", "--cli",
    "/usr/bin/env", "-i", `HOME=${temporary}`, "PATH=/nonexistent",
    `XDG_CONFIG_HOME=${temporary}/config`, `XDG_DATA_HOME=${temporary}/data`,
    `XDG_STATE_HOME=${temporary}/state`, "BUGSINK_DSN=", binary, "mcp",
    "--", "--method", method, "--format", "json", "--connect-timeout", "10000",
    "--cwd", temporary,
  ];
  const child = spawn("npx", args, { detached: true, stdio: ["ignore", "pipe", "pipe"] });
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8").on("data", data => { stdout += data; });
  child.stderr.setEncoding("utf8").on("data", data => { stderr += data; });
  const timer = setTimeout(() => {
    process.kill(-child.pid, "SIGKILL");
  }, 60000);
  try {
    await new Promise((resolve, reject) => {
      child.once("error", reject);
      child.once("close", (code, signal) => {
        if (code === 0) resolve();
        else reject(new Error(`Inspector ${method} failed (${signal ?? code}): ${stderr}`));
      });
    });
    return JSON.parse(stdout).result;
  } finally {
    clearTimeout(timer);
  }
}

try {
  const initialized = await inspect("initialize");
  assert(initialized.serverInfo?.name, "Missing server identity");
  assert(initialized.protocolVersion, "Missing negotiated protocol version");
  const result = await inspect("tools/list");
  assert(Array.isArray(result.tools), "Missing tools array");
  const tools = new Map(result.tools.map(tool => [tool.name, tool]));
  for (const name of expected.split(",")) {
    assert(tools.has(name), `Missing tool: ${name}`);
    assert.equal(tools.get(name).inputSchema?.type, "object", `Invalid schema: ${name}`);
  }
  assert.deepEqual(readdirSync(temporary), [], "Server created files during discovery");
  console.log(`MCP startup passed: ${initialized.serverInfo.name}, ${tools.size} tools`);
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
