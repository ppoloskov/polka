// Book fonts, bundled locally (all three are SIL OFL): the library is an
// offline server, so the reader never fetches type from a CDN. The
// cyrillic and latin subsets cover the languages in the catalog; importing
// the css files makes Vite bundle the referenced woff2 files.
import "@fontsource/literata/cyrillic-400.css";
import "@fontsource/literata/cyrillic-700.css";
import "@fontsource/literata/latin-400.css";
import "@fontsource/literata/latin-700.css";
import "@fontsource/pt-serif/cyrillic-400.css";
import "@fontsource/pt-serif/cyrillic-700.css";
import "@fontsource/pt-serif/latin-400.css";
import "@fontsource/pt-serif/latin-700.css";
import "@fontsource/noto-serif/cyrillic-400.css";
import "@fontsource/noto-serif/cyrillic-700.css";
import "@fontsource/noto-serif/latin-400.css";
import "@fontsource/noto-serif/latin-700.css";
