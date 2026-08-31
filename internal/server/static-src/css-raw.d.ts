// Vite's `?raw` suffix imports a file as a string. Declared locally instead of
// pulling in vite/client's full ambient types, which knip would flag as an
// unused dependency hint. Browser-project tests use it to inject the real
// stylesheets for computed-style and geometry assertions.
declare module "*.css?raw" {
  const content: string;
  export default content;
}
