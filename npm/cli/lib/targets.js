import { readFileSync } from "node:fs";

const targetFile = new URL("../targets.json", import.meta.url);
const parsedTargets = JSON.parse(readFileSync(targetFile, "utf8"));

export const targets = validateTargets(parsedTargets);

export function selectTarget(platform, arch) {
  const target = targets.find(
    (candidate) => candidate.platform === platform && candidate.arch === arch,
  );
  if (!target) {
    throw new Error(`Unsupported platform: ${platform} (${arch})`);
  }
  return target;
}

function validateTargets(value) {
  if (!Array.isArray(value) || value.length === 0) {
    throw new Error("Mnemon target registry is empty");
  }
  const ids = new Set();
  const runtimes = new Set();
  const aliases = new Set();
  return Object.freeze(
    value.map((target) => {
      for (const field of ["id", "platform", "arch", "goos", "goarch", "alias", "binary"]) {
        if (typeof target[field] !== "string" || target[field].length === 0) {
          throw new Error(`Mnemon target has invalid ${field}`);
        }
      }
      const runtime = `${target.platform}/${target.arch}`;
      if (ids.has(target.id) || runtimes.has(runtime) || aliases.has(target.alias)) {
        throw new Error(`Mnemon target registry contains a duplicate: ${target.id}`);
      }
      ids.add(target.id);
      runtimes.add(runtime);
      aliases.add(target.alias);
      return Object.freeze({ ...target });
    }),
  );
}
