import { lazy, type ComponentType } from "react";

/**
 * lazyNamed is React.lazy for modules that export a named component rather
 * than a default one, which is every panel in this app.
 *
 * React.lazy insists on a module whose `default` is the component, so without
 * this each call site would need its own `.then(m => ({ default: m.Thing }))`
 * wrapper. Keeping that in one place is what makes converting a panel to lazy
 * a one-line change.
 *
 * The import thunk must contain a literal `import()` at the call site (not a
 * computed path) so the bundler can see the dependency and emit a chunk for
 * it — passing a variable here would silently defeat code splitting.
 */
export function lazyNamed<K extends string, M extends Record<K, ComponentType<any>>>(
  load: () => Promise<M>,
  name: K,
) {
  return lazy(async () => ({ default: (await load())[name] }));
}
