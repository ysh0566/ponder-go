# Ponder browser entry

Build the Ponder 0.18.2 public entry as an ES module for browser bundlers.
The build uses the source retained in this repository and includes the Ponder
configuration, schema, checkpoint and ABI helpers. Runtime dependencies are
listed in `package.json`. TypeScript consumers can use the declarations from
`ponder@0.18.2` by installing the optional peer.

From this directory in a repository checkout, run `npm install` and
`npm run build`. The entry is `dist/index.js`. Use `npm pack` to create a
package that can be installed independently of the checkout. Run `npm test`
to exercise the original Vite browser build with in-memory ES output.
