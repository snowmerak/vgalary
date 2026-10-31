import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { install } from "./install.mjs";

function git(cwd, ...args) {
  const result = spawnSync("git", args, {
    cwd,
    encoding: "utf8",
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr);
}
function commit(cwd, message) {
  git(cwd, "add", ".");
  git(
    cwd,
    "-c",
    "user.name=Installer Test",
    "-c",
    "user.email=installer-test@example.invalid",
    "commit",
    "-m",
    message,
  );
}
function builder(version) {
  return `import { mkdirSync, writeFileSync } from 'node:fs';
mkdirSync('.deploy', { recursive: true });
writeFileSync('.deploy/' + (process.platform === 'win32' ? 'vgalary.exe' : 'vgalary'), ${JSON.stringify(version)});`;
}

test("installer preserves the installed executable across dirty checkouts and failed updates", async (t) => {
  const temporary = await mkdtemp(join(tmpdir(), "vgalary install test "));
  t.after(() => rm(temporary, { recursive: true, force: true }));
  const repository = join(temporary, "upstream");
  await mkdir(join(repository, "scripts"), { recursive: true });
  await writeFile(join(repository, ".gitignore"), ".deploy/\n");
  await writeFile(
    join(repository, "scripts", "deploy.mjs"),
    builder("version one"),
  );
  git(repository, "init");
  commit(repository, "Initial fixture");
  const directory = join(temporary, "installed with spaces");
  const binary = await install({ directory, repository });
  assert.equal(await readFile(binary, "utf8"), "version one");
  await writeFile(
    join(repository, "scripts", "deploy.mjs"),
    builder("version two"),
  );
  commit(repository, "Update fixture");
  await install({ directory, repository, update: true });
  assert.equal(await readFile(binary, "utf8"), "version two");
  const localChanges = join(directory, "source", "local-changes.txt");
  await writeFile(localChanges, "keep these changes");
  await assert.rejects(
    install({ directory, repository, update: true }),
    /local changes/,
  );
  assert.equal(await readFile(localChanges, "utf8"), "keep these changes");
  assert.equal(await readFile(binary, "utf8"), "version two");
  await rm(localChanges);
  await writeFile(
    join(repository, "scripts", "deploy.mjs"),
    "process.exit(7);",
  );
  commit(repository, "Failed build fixture");
  await assert.rejects(
    install({ directory, repository, update: true }),
    /status 7/,
  );
  assert.equal(await readFile(binary, "utf8"), "version two");
  git(
    join(directory, "source"),
    "remote",
    "set-url",
    "origin",
    "https://example.invalid/other.git",
  );
  await assert.rejects(
    install({ directory, repository, update: true }),
    /different repository/,
  );
  assert.equal(await readFile(binary, "utf8"), "version two");
});
