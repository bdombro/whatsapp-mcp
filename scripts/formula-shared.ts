import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { basename, join } from "node:path";

export type RepoKind = "formula" | "cask" | "library";
export type Platform = "macos" | "linux";
export type Bump = "major" | "minor" | "patch";

export interface RepoInfo {
  kind: RepoKind;
  name: string;
  rubyPath?: string;
  portManifestPath?: string;
}

export interface PlatformAsset {
  assetName: string;
  sha256: string;
}

export interface FormulaUpdateOptions {
  version: string;
  repoUrl: string;
  macos?: PlatformAsset;
  linux?: PlatformAsset;
}

export interface CaskUpdateOptions {
  version: string;
  repoUrl: string;
  assetName: string;
  sha256: string;
}

const SEMVER_RE = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

export function errorMessage(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  if (error instanceof Error && "stderr" in error && Buffer.isBuffer(error.stderr) && error.stderr.length) {
    return `${message}\n${error.stderr.toString().trim()}`;
  }
  return message;
}

export function assertSemver(version: string, label = "Version"): string {
  const trimmed = version.trim();
  if (!SEMVER_RE.test(trimmed)) {
    throw new Error(`${label} must contain a major.minor.patch version (got "${trimmed}").`);
  }
  return trimmed;
}

export function readVersion(versionPath: string): string {
  return assertSemver(readFileSync(versionPath, "utf8"), versionPath);
}

export function sha256File(path: string): string {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function evalJustVar(repoRoot: string, name: string): string | null {
  const result = Bun.spawnSync(["just", "--evaluate", name], {
    cwd: repoRoot,
    stdout: "pipe",
    stderr: "pipe",
  });
  if (result.exitCode !== 0) return null;
  return result.stdout.toString().trim();
}

export function detectRepo(repoRoot: string): RepoInfo {
  const formulaDir = join(repoRoot, "Formula");
  const caskDir = join(repoRoot, "Casks");
  const portsDir = join(repoRoot, "ports");

  const formulas = existsSync(formulaDir)
    ? readdirSync(formulaDir).filter((file) => file.endsWith(".rb")).sort()
    : [];
  const casks = existsSync(caskDir)
    ? readdirSync(caskDir).filter((file) => file.endsWith(".rb")).sort()
    : [];
  const ports = existsSync(portsDir)
    ? readdirSync(portsDir, { withFileTypes: true })
        .filter((entry) => entry.isDirectory() && existsSync(join(portsDir, entry.name, "vcpkg.json")))
        .map((entry) => entry.name)
        .sort()
    : [];

  if (formulas.length > 0) {
    if (formulas.length !== 1) {
      throw new Error(`Expected exactly 1 Formula/*.rb file, found ${formulas.length}.`);
    }
    const name = formulas[0].slice(0, -".rb".length);
    return { kind: "formula", name, rubyPath: join(formulaDir, formulas[0]) };
  }

  if (casks.length > 0) {
    if (casks.length !== 1) {
      throw new Error(`Expected exactly 1 Casks/*.rb file, found ${casks.length}.`);
    }
    const name = casks[0].slice(0, -".rb".length);
    return { kind: "cask", name, rubyPath: join(caskDir, casks[0]) };
  }

  if (ports.length === 0) {
    throw new Error(
      `Cannot determine repo kind in ${repoRoot} (found Formula=${formulas.length}, Casks=${casks.length}, ports=${ports.length}).`,
    );
  }

  if (ports.length === 1) {
    const name = ports[0];
    return { kind: "library", name, portManifestPath: join(portsDir, name, "vcpkg.json") };
  }

  let selected = evalJustVar(repoRoot, "formula");
  if (!selected && existsSync(join(repoRoot, "vcpkg.json"))) {
    const rootManifest = JSON.parse(readFileSync(join(repoRoot, "vcpkg.json"), "utf8")) as { name?: unknown };
    if (typeof rootManifest.name === "string" && rootManifest.name.trim()) {
      selected = rootManifest.name.trim();
    }
  }
  if (!selected) selected = basename(repoRoot);
  if (!ports.includes(selected)) {
    throw new Error(`Ambiguous multi-port library (${ports.join(", ")}); resolved "${selected}" is not under ports/.`);
  }
  return { kind: "library", name: selected, portManifestPath: join(portsDir, selected, "vcpkg.json") };
}

export function readPlatforms(repoRoot: string, kind: RepoKind): Platform[] {
  const raw = evalJustVar(repoRoot, "platforms");
  if (!raw) throw new Error("just --evaluate platforms failed or returned empty.");
  const entries = raw.split(/\s+/).filter(Boolean);
  if (!entries.length) throw new Error("platforms justfile variable must not be empty.");
  const platforms: Platform[] = [];
  for (const entry of entries) {
    if (entry !== "macos" && entry !== "linux") {
      throw new Error(`Unknown platform "${entry}" in justfile platforms.`);
    }
    if (!platforms.includes(entry)) platforms.push(entry);
  }
  if (kind === "cask" && (platforms.length !== 1 || platforms[0] !== "macos")) {
    throw new Error(`Cask repositories only support macos (got "${platforms.join(" ")}").`);
  }
  return platforms;
}

export function resolveGitHubRepo(originUrl: string, fallbackRubyContent?: string): string | null {
  const trimmed = originUrl.trim();
  const match = trimmed.match(/github\.com[:/]([^/\s]+\/[^/\s]+?)(?:\.git)?$/i);
  if (match) return match[1];
  if (fallbackRubyContent) {
    const homepage = fallbackRubyContent.match(/^\s*homepage\s+["']https:\/\/github\.com\/([^/"'\s]+\/[^/"'\s]+?)(?:\.git)?["']/m);
    if (homepage) return homepage[1];
  }
  return null;
}

export function assetFilename(name: string, kind: RepoKind, platform: Platform, ext?: string): string {
  if (kind === "cask") {
    if (!ext) throw new Error("Cask asset extension is required.");
    return `${name}-arm64-macos.${ext}`;
  }
  return platform === "macos" ? `${name}-arm64-macos.tar.gz` : `${name}-x64-linux.tar.gz`;
}

// JSON strings are valid Ruby literals; escape Ruby's interpolation separately.
function rubyString(value: string): string {
  return JSON.stringify(value).replaceAll("#{", "\\#{");
}

export function updateFormulaRuby(content: string, options: FormulaUpdateOptions): string {
  const normalized = content.replaceAll("\r\n", "\n");
  const bodyMatch = normalized.match(/^[ \t]*(?:def |service do\b|test do\b)/m);
  const splitIndex = bodyMatch?.index ?? normalized.length;
  let header = normalized.slice(0, splitIndex);
  const tail = normalized.slice(splitIndex);

  header = header.replace(/^[ \t]*on_macos\s+do[\s\S]*?^[ \t]*end[ \t]*\n?/gm, "");
  header = header.replace(/^[ \t]*on_linux\s+do[\s\S]*?^[ \t]*end[ \t]*\n?/gm, "");
  header = header.replace(/^[ \t]*url\s+["'][^"']*["'][ \t]*\n?/gm, "");
  header = header.replace(/^[ \t]*sha256\s+["'][^"']*["'][ \t]*\n?/gm, "");

  if (/^[ \t]*version\s+["'][^"']*["']/m.test(header)) {
    header = header.replace(/^([ \t]*version\s+)["'][^"']*["']/m, `$1${rubyString(options.version)}`);
  } else if (/^[ \t]*homepage\s+["'][^"']*["'][ \t]*$/m.test(header)) {
    header = header.replace(/^([ \t]*homepage\s+["'][^"']*["'][ \t]*)$/m, `$1\n  version ${rubyString(options.version)}`);
  } else {
    header = header.replace(/^(class\s+\S+.*\n)/m, `$1  version ${rubyString(options.version)}\n`);
  }

  const blocks: string[] = [];
  if (options.macos) {
    blocks.push(
      `  on_macos do\n    url ${rubyString(`${options.repoUrl}/releases/download/v${options.version}/${options.macos.assetName}`)}\n    sha256 ${rubyString(options.macos.sha256)}\n  end`,
    );
  }
  if (options.linux) {
    blocks.push(
      `  on_linux do\n    url ${rubyString(`${options.repoUrl}/releases/download/v${options.version}/${options.linux.assetName}`)}\n    sha256 ${rubyString(options.linux.sha256)}\n  end`,
    );
  }

  if (blocks.length) {
    const versionLine = /^([ \t]*version\s+["'][^"']*["'][ \t]*)$/m;
    if (versionLine.test(header)) {
      header = header.replace(versionLine, `$1\n\n${blocks.join("\n\n")}`);
    } else {
      header = `${header.trimEnd()}\n\n${blocks.join("\n\n")}\n`;
    }
  }

  header = header.replace(/\n{3,}/g, "\n\n");
  if (tail && !header.endsWith("\n\n")) {
    header = `${header.trimEnd()}\n\n`;
  }
  return `${header}${tail}`;
}

export function updateCaskRuby(content: string, options: CaskUpdateOptions): string {
  const normalized = content.replaceAll("\r\n", "\n");
  const bodyMatch = normalized.match(/^[ \t]*(?:pkg |app |postflight\b|postflight_steps\b|uninstall\b|uninstall_preflight_steps\b|caveats\b)/m);
  const splitIndex = bodyMatch?.index ?? normalized.length;
  let header = normalized.slice(0, splitIndex);
  const tail = normalized.slice(splitIndex);

  const assetUrl = `${options.repoUrl}/releases/download/v${options.version}/${options.assetName}`;
  if (!/^[ \t]*version\s+["'][^"']*["']/m.test(header)) {
    throw new Error("Cask file must contain a version field.");
  }
  if (!/^[ \t]*sha256\s+["'][^"']*["']/m.test(header)) {
    throw new Error("Cask file must contain a sha256 field.");
  }
  if (!/^[ \t]*url\s+["'][^"']*["']/m.test(header)) {
    throw new Error("Cask file must contain a url field.");
  }

  header = header.replace(/^([ \t]*version\s+)["'][^"']*["']/m, `$1${rubyString(options.version)}`);
  header = header.replace(/^([ \t]*sha256\s+)["'][^"']*["']/m, `$1${rubyString(options.sha256)}`);
  header = header.replace(/^([ \t]*url\s+)["'][^"']*["']/m, `$1${rubyString(assetUrl)}`);
  return `${header}${tail}`;
}

export function applyBump(version: string, bump: Bump): string {
  const parts = assertSemver(version).split(".").map(Number);
  if (bump === "major") return `${parts[0] + 1}.0.0`;
  if (bump === "minor") return `${parts[0]}.${parts[1] + 1}.0`;
  return `${parts[0]}.${parts[1]}.${parts[2] + 1}`;
}

export function promoteChangelog(content: string, version: string, date: string): { changelog: string; notes: string } {
  const normalized = content.replaceAll("\r\n", "\n");
  const heading = /^## Unreleased[ \t]*$/gm;
  const matches = [...normalized.matchAll(heading)];
  if (matches.length !== 1) throw new Error("CHANGELOG.md must have exactly one ## Unreleased heading.");
  const match = matches[0];
  const body = normalized.slice(match.index + match[0].length).split(/^## /m)[0].trim();
  if (!body) throw new Error("Add release notes under ## Unreleased before releasing.");
  return {
    changelog: normalized.replace(heading, `## Unreleased\n\n## v${version} - ${date}`),
    notes: `${body}\n`,
  };
}
