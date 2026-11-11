#!/usr/bin/env node
// Homebrew tap + local binary tarball staging helper for whatsapp-client & whatsapp-mcp.
// Links $(brew --prefix)/Library/Taps/bdombro/homebrew-whatsapp-mcp -> repo root,
// builds outside Homebrew superenv, stages Formula/.staging/<name>-local.tar.gz,
// installs/reinstalls bdombro/whatsapp-mcp/<formula>, and always restores Formula/*.rb.

import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const TAP_NAME = "bdombro/whatsapp-mcp";
const TAP_SHORT_DIR = "homebrew-whatsapp-mcp";
const STAGING_DIR = path.join(REPO_ROOT, "Formula", ".staging");
const ENDPOINT_URL = "http://127.0.0.1:8088/.well-known/oauth-authorization-server";

const FORMULAS = {
  "whatsapp-client": {
    name: "whatsapp-client",
    formulaPath: path.join(REPO_ROOT, "Formula", "whatsapp-client.rb"),
    binaryPath: path.join(REPO_ROOT, "client", "dist", "whatsapp-client"),
    buildArgs: ["--justfile", "client/justfile", "build"],
  },
  "whatsapp-mcp": {
    name: "whatsapp-mcp",
    formulaPath: path.join(REPO_ROOT, "Formula", "whatsapp-mcp.rb"),
    binaryPath: path.join(REPO_ROOT, "mcp", "build", "bin", "whatsapp-mcp"),
    buildArgs: ["--justfile", "mcp/justfile", "build"],
  },
};

function run(cmd, args, opts = {}) {
  return execFileSync(cmd, args, {
    cwd: REPO_ROOT,
    stdio: opts.capture ? ["ignore", "pipe", "pipe"] : "inherit",
    encoding: "utf8",
    ...opts,
  });
}

function brewEnv() {
  return {
    ...process.env,
    HOMEBREW_NO_AUTO_UPDATE: "1",
    HOMEBREW_NO_ASK: "1",
  };
}

function brewPrefix() {
  return run("brew", ["--prefix"], { capture: true }).trim();
}

function tapDir() {
  return path.join(brewPrefix(), "Library", "Taps", "bdombro", TAP_SHORT_DIR);
}

function ensureTapLinked() {
  const dir = tapDir();
  fs.mkdirSync(path.dirname(dir), { recursive: true });
  try {
    const stat = fs.lstatSync(dir);
    if (stat.isSymbolicLink() && fs.readlinkSync(dir) === REPO_ROOT) {
      return;
    }
    fs.rmSync(dir, { recursive: true, force: true });
  } catch {
    // Not linked yet
  }
  fs.symlinkSync(REPO_ROOT, dir);
}

function refuseNonTapConflict(formulaName) {
  try {
    const out = run("brew", ["info", "--json=v2", formulaName], {
      capture: true,
      env: brewEnv(),
    });
    const info = JSON.parse(out);
    const f = info?.formulae?.[0];
    if (f && f.installed?.length > 0 && f.tap && f.tap !== TAP_NAME) {
      throw new Error(
        `Refusing to proceed: ${formulaName} is already installed from ${f.tap}. Uninstall it before using ${TAP_NAME}.`,
      );
    }
  } catch (err) {
    if (err.message?.includes("Refusing to proceed")) throw err;
  }
}

function isServiceStarted(formulaName) {
  try {
    const out = run("brew", ["services", "info", formulaName, "--json"], {
      capture: true,
      env: brewEnv(),
    });
    const info = JSON.parse(out);
    const entry = Array.isArray(info) ? info[0] : info;
    return Boolean(entry?.running || entry?.loaded);
  } catch {
    return false;
  }
}

function createLocalTarball(spec) {
  if (!fs.existsSync(spec.binaryPath)) {
    throw new Error(`Missing built binary at ${spec.binaryPath}. Run 'just build' first.`);
  }
  fs.mkdirSync(STAGING_DIR, { recursive: true });
  const pkgDir = path.join(STAGING_DIR, spec.name);
  fs.rmSync(pkgDir, { recursive: true, force: true });
  fs.mkdirSync(pkgDir, { recursive: true });
  const destBin = path.join(pkgDir, spec.name);
  fs.copyFileSync(spec.binaryPath, destBin);
  fs.chmodSync(destBin, 0o755);

  const tarball = path.join(STAGING_DIR, `${spec.name}-local.tar.gz`);
  run("tar", ["-czf", tarball, "-C", pkgDir, "."]);
  const sha256 = createHash("sha256").update(fs.readFileSync(tarball)).digest("hex");
  return { tarball, sha256 };
}

function rewriteFormulaForLocal(original, tarball, sha256, formulaPath) {
  let updated = original.replace(/^(\s*url\s+)"[^"]+"/m, `$1"file://${tarball}"`);
  updated = updated.replace(/^(\s*sha256\s+)"[^"]+"/m, `$1"${sha256}"`);
  if (updated === original) {
    throw new Error(`Failed to stage url/sha256 in ${formulaPath}`);
  }
  return updated;
}

async function waitForMcpEndpoint(timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(ENDPOINT_URL);
      if (res.ok) {
        const meta = await res.json();
        if (meta?.token_endpoint) return meta;
      }
    } catch {
      // Retry until deadline
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`Timed out waiting for ${ENDPOINT_URL}`);
}

function selectFormulas(args) {
  const names = args.filter((a) => a.length > 0);
  if (names.length === 0) {
    // Default to whatsapp-mcp only so local MCP installs never touch whatsapp-client daemon.
    return [FORMULAS["whatsapp-mcp"]];
  }
  return names.map((n) => {
    const spec = FORMULAS[n];
    if (!spec) {
      throw new Error(`Unknown formula '${n}'. Expected whatsapp-mcp or whatsapp-client.`);
    }
    return spec;
  });
}

async function runWithStagedFormulas(selected, fn) {
  const backups = new Map();
  for (const spec of selected) {
    backups.set(spec.formulaPath, fs.readFileSync(spec.formulaPath, "utf8"));
  }
  let restored = false;
  const restore = () => {
    if (restored) return;
    restored = true;
    for (const [p, orig] of backups.entries()) {
      fs.writeFileSync(p, orig, "utf8");
    }
    fs.rmSync(STAGING_DIR, { recursive: true, force: true });
  };

  process.on("SIGINT", () => {
    restore();
    process.exit(130);
  });
  process.on("SIGTERM", () => {
    restore();
    process.exit(143);
  });

  try {
    for (const spec of selected) {
      const { tarball, sha256 } = createLocalTarball(spec);
      const orig = backups.get(spec.formulaPath);
      fs.writeFileSync(
        spec.formulaPath,
        rewriteFormulaForLocal(orig, tarball, sha256, spec.formulaPath),
        "utf8",
      );
    }
    await fn();
  } finally {
    restore();
  }
}

const [, , action, ...rest] = process.argv;
const selected = selectFormulas(rest);

switch (action) {
  case "check": {
    for (const spec of selected) {
      run("just", spec.buildArgs);
    }
    await runWithStagedFormulas(selected, async () => {
      for (const spec of selected) {
        run("ruby", ["-c", spec.formulaPath]);
      }
      console.log(`Validated staged tap formulas: ${selected.map((s) => s.name).join(", ")}`);
    });
    break;
  }
  case "install": {
    ensureTapLinked();
    for (const spec of selected) {
      refuseNonTapConflict(spec.name);
      run("brew", ["install", `${TAP_NAME}/${spec.name}`], { env: brewEnv() });
    }
    break;
  }
  case "install-local":
  case "reinstall-local": {
    const isReinstall = action === "reinstall-local";
    ensureTapLinked();
    for (const spec of selected) {
      refuseNonTapConflict(spec.name);
      run("just", spec.buildArgs);
    }

    const wasRunning = new Map();
    if (isReinstall) {
      for (const spec of selected) {
        if (spec.name === "whatsapp-mcp") {
          const running = isServiceStarted(`${TAP_NAME}/whatsapp-mcp`) || isServiceStarted("whatsapp-mcp");
          wasRunning.set(spec.name, running);
          if (running) {
            run("brew", ["services", "stop", `${TAP_NAME}/whatsapp-mcp`], { env: brewEnv() });
          }
        }
      }
    }

    await runWithStagedFormulas(selected, async () => {
      for (const spec of selected) {
        const verb = isReinstall ? "reinstall" : "install";
        run("brew", [verb, "--formula", `${TAP_NAME}/${spec.name}`], { env: brewEnv() });
      }
    });

    if (isReinstall && wasRunning.get("whatsapp-mcp")) {
      run("brew", ["services", "start", `${TAP_NAME}/whatsapp-mcp`], { env: brewEnv() });
      await waitForMcpEndpoint();
      console.log("whatsapp-mcp restarted and ready at http://127.0.0.1:8088/mcp");
    }
    break;
  }
  default:
    console.error(
      "Usage: node scripts/dev-formula.mjs [check|install|install-local|reinstall-local] [whatsapp-mcp|whatsapp-client...]",
    );
    process.exit(1);
}
