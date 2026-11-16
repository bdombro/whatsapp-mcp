import { existsSync, readdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { $ } from "bun";
import {
  applyBump, assertSemver, assetFilename, type Bump, errorMessage, type PlatformAsset,
  promoteChangelog, readPlatforms, readVersion, resolveGitHubRepo, sha256File, updateFormulaRuby,
} from "./formula-shared.ts";

type Command =
  | { kind: "next"; bump: Bump }
  | { kind: "publish"; version: string; assetsDir: string };

export function parseArgs(args: string[]): Command {
  if (args[0] === "next" && args.length === 2) {
    const bump = args[1];
    if (bump === "major" || bump === "minor" || bump === "patch") {
      return { kind: "next", bump };
    }
  }
  if (args[0] === "publish" && (args.length === 2 || args.length === 3)) {
    return { kind: "publish", version: args[1], assetsDir: args[2] ?? "dist" };
  }
  throw new Error("Usage: release.ts <next <major|minor|patch> | publish <version> [assets-dir]>");
}

// Repo-owned: one VERSION drives every Formula/*.rb (whatsapp-client + whatsapp-mcp)
function listFormulas(repoRoot: string): string[] {
  const formulas = readdirSync(join(repoRoot, "Formula"))
    .filter((file) => file.endsWith(".rb"))
    .sort()
    .map((file) => file.slice(0, -".rb".length));
  if (!formulas.length) throw new Error("Expected at least 1 Formula/*.rb file.");
  return formulas;
}

async function runNext(repoRoot: string, bump: Bump): Promise<void> {
  listFormulas(repoRoot);
  const current = readVersion(join(repoRoot, "VERSION"));
  const nextVersion = applyBump(current, bump);
  const changelogPath = join(repoRoot, "CHANGELOG.md");
  promoteChangelog(readFileSync(changelogPath, "utf8"), nextVersion, new Date().toISOString().slice(0, 10));
  process.stdout.write(`${nextVersion}\n`);
}

async function runPublish(repoRoot: string, versionArg: string, assetsDirArg: string): Promise<void> {
  const version = assertSemver(versionArg, "Release version");
  const tag = `v${version}`;
  const formulas = listFormulas(repoRoot);
  readVersion(join(repoRoot, "VERSION"));

  const changelogPath = join(repoRoot, "CHANGELOG.md");
  const { changelog, notes } = promoteChangelog(
    readFileSync(changelogPath, "utf8"),
    version,
    new Date().toISOString().slice(0, 10),
  );

  const top = (await $`git rev-parse --show-toplevel`.cwd(repoRoot).quiet().text()).trim();
  if (realpathSync(top) !== realpathSync(repoRoot)) {
    throw new Error("Run releases from the repository root.");
  }
  await $`git rev-parse --verify HEAD`.cwd(repoRoot).quiet();
  const origin = (await $`git remote get-url origin`.cwd(repoRoot).quiet().text()).trim();
  const branch = (await $`git symbolic-ref --short HEAD`.cwd(repoRoot).quiet().text()).trim();

  const localTag = await $`git show-ref --verify --quiet ${`refs/tags/${tag}`}`.cwd(repoRoot).nothrow().quiet();
  if (localTag.exitCode === 0) throw new Error(`${tag} already exists locally.`);
  if (localTag.exitCode !== 1) throw new Error(`Cannot check local tag ${tag}.`);
  const remoteTag = await $`git ls-remote --exit-code origin ${`refs/tags/${tag}`}`.cwd(repoRoot).nothrow().quiet();
  if (remoteTag.exitCode === 0) throw new Error(`${tag} already exists on origin.`);
  if (remoteTag.exitCode !== 2) throw new Error(`Cannot check remote tag ${tag}: ${remoteTag.stderr.toString()}`);

  const assetsDir = resolve(repoRoot, assetsDirArg);
  const assetPaths: string[] = [];
  let ownerRepo: string | null = null;

  const platforms = readPlatforms(repoRoot, "formula");
  const rubyUpdates: { path: string; content: string }[] = [];
  for (const name of formulas) {
    const rubyPath = join(repoRoot, "Formula", `${name}.rb`);
    const rubyContent = readFileSync(rubyPath, "utf8");
    ownerRepo ??= resolveGitHubRepo(origin, rubyContent);
    if (!ownerRepo) throw new Error("Cannot determine GitHub owner/repo from origin or Formula homepage.");
    const repoUrl = `https://github.com/${ownerRepo}`;

    let macosAsset: PlatformAsset | undefined;
    let linuxAsset: PlatformAsset | undefined;
    for (const platform of platforms) {
      const assetName = assetFilename(name, "formula", platform);
      const assetPath = join(assetsDir, assetName);
      if (!existsSync(assetPath)) throw new Error(`Missing release asset: ${assetPath}`);
      assetPaths.push(assetPath);
      const asset = { assetName, sha256: sha256File(assetPath) };
      if (platform === "macos") macosAsset = asset;
      else linuxAsset = asset;
    }
    rubyUpdates.push({
      path: rubyPath,
      content: updateFormulaRuby(rubyContent, { version, repoUrl, macos: macosAsset, linux: linuxAsset }),
    });
  }

  writeFileSync(join(repoRoot, "VERSION"), `${version}\n`);
  writeFileSync(changelogPath, changelog);
  for (const update of rubyUpdates) writeFileSync(update.path, update.content);
  await $`git add -- VERSION CHANGELOG.md ${formulas.map((name) => `Formula/${name}.rb`)}`.cwd(repoRoot).quiet();
  await $`git commit -m ${`Release ${tag}`}`.cwd(repoRoot).quiet();

  await $`git tag -a ${tag} -m ${tag}`.cwd(repoRoot).quiet();
  await $`git push --atomic origin ${`HEAD:refs/heads/${branch}`} ${`refs/tags/${tag}`}`.cwd(repoRoot).quiet();

  const notesPath = join(repoRoot, ".git", "release-notes.md");
  writeFileSync(notesPath, notes);
  try {
    const ghArgs = [
      "release",
      "create",
      tag,
      ...assetPaths,
      ...(ownerRepo ? ["--repo", ownerRepo] : []),
      "--verify-tag",
      "--title",
      tag,
      "--notes-file",
      notesPath,
    ];
    await $`gh ${ghArgs}`.cwd(repoRoot).quiet();
  } finally {
    rmSync(notesPath, { force: true });
  }
  console.log(`Released ${tag}`);
}

async function main(): Promise<void> {
  const command = parseArgs(process.argv.slice(2));
  const repoRoot = process.cwd();
  if (command.kind === "next") {
    await runNext(repoRoot, command.bump);
  } else {
    await runPublish(repoRoot, command.version, command.assetsDir);
  }
}

if (import.meta.main) {
  await main().catch((error: unknown) => {
    console.error(`Release stopped: ${errorMessage(error)}`);
    process.exitCode = 1;
  });
}
