import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const extensionURI = "https://tangxusc.github.io/block-play-table/a2a/extensions/execution/v1";
const extensionDir = resolve(root, "docs/a2a/extensions/execution/v1");
const schemaFiles = ["request.schema.json", "event.schema.json", "artifact.schema.json"];

for (const filename of schemaFiles) {
  const source = await readFile(resolve(extensionDir, filename), "utf8");
  const schema = JSON.parse(source);
  const expectedID = `${extensionURI}/${filename}`;
  if (schema.$id !== expectedID) {
    throw new Error(`${filename} 的 $id 必须为 ${expectedID}`);
  }
  if (schema.$schema !== "https://json-schema.org/draft/2020-12/schema") {
    throw new Error(`${filename} 必须使用 JSON Schema draft 2020-12`);
  }
}

const indexHTML = await readFile(resolve(extensionDir, "index.html"), "utf8");
for (const filename of schemaFiles) {
  if (!indexHTML.includes(`href="${filename}"`)) {
    throw new Error(`index.html 缺少 ${filename} 链接`);
  }
}

process.stdout.write(`A2A execution v1 扩展资源验证通过：${extensionURI}\n`);
