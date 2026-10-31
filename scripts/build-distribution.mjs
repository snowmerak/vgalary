#!/usr/bin/env node
import { copyFile, mkdir, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const root = fileURLToPath(new URL("../", import.meta.url));
const output = join(root, ".deploy", "pages");
await mkdir(output, { recursive: true });
for (const name of ["index.html", "styles.css", "404.html", "_headers"])
  await copyFile(join(root, "site", name), join(output, name));
await copyFile(
  join(root, "scripts", "install.mjs"),
  join(output, "install.mjs"),
);
await writeFile(join(output, "robots.txt"), "User-agent: *\nAllow: /\n");
console.log(`Cloudflare Pages static files: ${output}`);
