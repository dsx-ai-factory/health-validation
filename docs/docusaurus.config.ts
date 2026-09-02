import type * as Preset from "@docusaurus/preset-classic";
import type { Config } from "@docusaurus/types";
import { themes as prismThemes } from "prism-react-renderer";

const config: Config = {
  title: "health-validation",
  tagline: "Hardware health check catalog and evaluation engine for NVIDIA GPU nodes",

  url: "https://dsx-ai-factory.github.io",
  // The Pages site is access-controlled while this repository is internal, and
  // GitHub serves those from the root of a generated subdomain rather than under
  // /<repo>/. Set this back to "/health-validation/" when the repository goes
  // public; the Pages workflow fails the build if the two disagree.
  baseUrl: "/",

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
