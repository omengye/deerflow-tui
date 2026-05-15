const { getAsset } = require("node:sea");

const entrySource = getAsset("entry.mjs", "utf8");
const entryUrl = `data:text/javascript;base64,${Buffer.from(entrySource).toString("base64")}`;

import(entryUrl).catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
