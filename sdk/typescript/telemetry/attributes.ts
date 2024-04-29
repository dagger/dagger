/**
 * Replace parent span (e.g., `exec /runtime`).
 */
export const UI_MASK = "dagger.io/ui.mask"

/**
 * Reveal only child spans (e.g., `typescript runtime execution` parent span).
 */
export const UI_PASSTHROUGH = "dagger.io/ui.passthrough"

/**
 * Hide children by default (e.g., test case that runs pipelines).
 */
export const UI_ENCAPSULATE = "dagger.io/ui.encapsulate"
