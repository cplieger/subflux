// Vite's `?raw` suffix imports a file as a string. Declared locally instead of
// pulling in vite/client's full ambient types, which knip would flag as an
// unused dependency hint. Browser-project tests use it to inject the real
// stylesheets for computed-style and geometry assertions, and to read the JSON
// fixtures the Go suite writes into testdata/.
declare module "*.css?raw" {
  const content: string;
  export default content;
}

declare module "*.json?raw" {
  const content: string;
  export default content;
}
