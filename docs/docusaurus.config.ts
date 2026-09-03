import type * as Preset from "@docusaurus/preset-classic";
import type { Config } from "@docusaurus/types";
import { themes as prismThemes } from "prism-react-renderer";

const config: Config = {
  title: "health-validation",
  tagline: "Hardware health check catalog and evaluation engine for NVIDIA GPU nodes",

  // url and baseUrl together form the origin Docusaurus bakes into canonical
  // links and sitemap.xml. The Pages site is access-controlled and served
  // from a generated per-run subdomain while this repository is internal,
  // not from dsx-ai-factory.github.io, and moves under /<repo>/ instead of
  // "/" once the repository (and the Pages site) goes public. The Pages
  // workflow gets both from actions/configure-pages' own origin and
  // base_path outputs and passes them through as DOCS_URL/DOCS_BASE_URL, so
  // they always match what Pages actually serves. Local/non-Pages builds
  // fall back to the eventual public origin and the site root.
  url: process.env.DOCS_URL || "https://dsx-ai-factory.github.io",
  baseUrl: process.env.DOCS_BASE_URL || "/",

  onBrokenLinks: "throw",

  markdown: {
    hooks: {
      onBrokenMarkdownLinks: "throw",
    },
  },

  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },

  plugins: ["./plugins/health-data"],

  presets: [
    [
      "classic",
      {
        docs: {
          sidebarPath: "./sidebars.ts",
          routeBasePath: "/",
        },
        blog: false,
        theme: {
          customCss: "./src/css/custom.css",
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    navbar: {
      title: "health-validation",
      items: [
        {
          type: "doc",
          docId: "intro",
          position: "left",
          label: "Docs",
        },
      ],
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
