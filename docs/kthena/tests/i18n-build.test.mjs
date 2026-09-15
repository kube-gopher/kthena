import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { test } from 'node:test';

const [defaultVersion] = JSON.parse(
  readFileSync(new URL('../versions.json', import.meta.url), 'utf8'),
);

// Check the actual output of the production build, including plugin-generated
// routes and theme overrides. Run after `npm run build` (or via make test-docs).
function page(route) {
  const file = route.endsWith('/') ? `${route}index.html` : `${route}.html`;
  return readFileSync(new URL(`../build${file}`, import.meta.url), 'utf8');
}

function tags(html, name) {
  return html.match(new RegExp(`<${name}\\b[^>]*>`, 'g')) ?? [];
}

function attribute(tag, name) {
  const match = tag.match(
    new RegExp(`\\s${name}=(?:"([^"]*)"|'([^']*)'|([^\\s>]+))`),
  );
  return match?.[1] ?? match?.[2] ?? match?.[3];
}

function hasLink(html, href, language) {
  return tags(html, 'a').some(
    (tag) =>
      attribute(tag, 'href') === href &&
      (!language || attribute(tag, 'lang') === language),
  );
}

test('both homepages render translated content and lead to the default release', () => {
  const en = page('/');
  const zh = page('/zh-Hans/');
  assert.equal(attribute(tags(en, 'html')[0], 'lang'), 'en');
  assert.equal(attribute(tags(zh, 'html')[0], 'lang'), 'zh-Hans');
  assert.match(en, /Get Started with Kthena/);
  assert.match(zh, /开始使用 Kthena/);
  assert.match(zh, /智能路由/);
  assert.match(zh, /分层 PD 分离编排/);
  assert.match(zh, new RegExp(`版权所有 © ${new Date().getFullYear()}`));
  assert.ok(hasLink(en, '/docs/intro'));
  assert.ok(hasLink(zh, '/zh-Hans/docs/intro'));
  assert.ok(hasLink(en, '/zh-Hans/', 'zh-Hans'));
  assert.ok(hasLink(zh, '/', 'en'));
});

const translatedDocs = [
  { doc: 'intro' },
  { doc: 'getting-started/installation' },
  {
    doc: 'getting-started/quick-start',
    omittedHeadingIds: ['modelbooster'],
  },
  { doc: 'architecture/architecture', slug: 'architecture', extension: '.mdx' },
  { doc: 'architecture/autoscaler', extension: '.mdx' },
  { doc: 'architecture/kthena-router' },
  { doc: 'architecture/model-serving-controller', extension: '.mdx' },
  { doc: 'developer-guide/ci' },
  { doc: 'developer-guide/development-setup' },
  { doc: 'developer-guide/release' },
  { doc: 'general/faq' },
  { doc: 'timeline/releases' },
  { doc: 'timeline/roadmap' },
];

for (const version of ['', 'next/']) {
  const docs = [...translatedDocs];
  if (version === '') docs.push({ doc: 'faq' });
  if (version === 'next/')
    docs.push({ doc: 'getting-started/gpu-free-quick-start' });
  for (const {
    doc,
    slug = doc,
    extension = '.md',
    omittedHeadingIds = [],
  } of docs) {
    const route = `/docs/${version}${slug}`;
    test(`translated document preserves navigation and section links: ${route}`, () => {
      const en = page(route);
      const zh = page(`/zh-Hans${route}`);
      assert.ok(hasLink(en, `/zh-Hans${route}`, 'zh-Hans'));
      assert.ok(hasLink(zh, route, 'en'));
      assert.doesNotMatch(zh, /此页面尚未翻译/);
      if (doc !== 'faq') assert.match(zh, /用户指南/);

      const headings = (html) =>
        tags(html, 'h[2-6]').map((tag) => attribute(tag, 'id'));
      assert.deepEqual(
        headings(zh),
        headings(en).filter((id) => !omittedHeadingIds.includes(id)),
        'section IDs must survive a language switch',
      );

      const links = tags(zh, 'link');
      assert.ok(
        links.some(
          (tag) =>
            attribute(tag, 'rel') === 'canonical' &&
            attribute(tag, 'href') ===
              `https://kthena.volcano.sh/zh-Hans${route}`,
        ),
      );
      assert.ok(
        links.some(
          (tag) =>
            attribute(tag, 'hreflang') === 'en' &&
            attribute(tag, 'href') === `https://kthena.volcano.sh${route}`,
        ),
      );
      const sourceVersion = version ? 'current' : `version-${defaultVersion}`;
      assert.ok(
        hasLink(
          zh,
          `https://github.com/volcano-sh/kthena/tree/main/docs/kthena/i18n/zh-Hans/docusaurus-plugin-content-docs/${sourceVersion}/${doc}${extension}`,
        ),
        'edit links must point to the translated source',
      );

      for (const image of tags(zh, 'img')) {
        const src = attribute(image, 'src');
        if (src?.startsWith('/')) {
          assert.ok(
            existsSync(new URL(`../build${src}`, import.meta.url)),
            `missing image ${src}`,
          );
        }
      }
    });
  }
}

test('links work in both directions between translated and fallback documents', () => {
  for (const version of ['', 'next/']) {
    const base = `/zh-Hans/docs/${version}`;
    assert.ok(
      hasLink(
        page(`${base}getting-started/installation`),
        `${base}general/cert-manager`,
      ),
    );
    assert.ok(
      hasLink(
        page(`${base}user-guide/gateway-api-support`),
        `${base}getting-started/installation`,
      ),
    );
    assert.ok(
      hasLink(
        page(`${base}reference/kthena-cli`),
        `${base}getting-started/installation#kthena-cli`,
      ),
    );
  }
});

test('untranslated docs and older versions keep their English content with a notice', () => {
  for (const route of [
    '/docs/general/cert-manager',
    '/docs/next/general/cert-manager',
    '/docs/architecture/model-booster-controller',
    '/docs/next/architecture/model-booster-controller',
    '/docs/v0.4.0/intro',
  ]) {
    const en = page(route);
    const zh = page(`/zh-Hans${route}`);
    assert.doesNotMatch(en, /此页面尚未翻译|Translation unavailable/);
    assert.match(zh, /此页面尚未翻译/);
    assert.ok(tags(zh, 'div').some((tag) => attribute(tag, 'lang') === 'en'));
    assert.ok(hasLink(zh, route, 'en'));
  }
});

const translatedPosts = [
  {
    slug: 'scoreplugin-benchmark-blog-post',
    source: '2025-09-09-benchmark/index.md',
  },
  { slug: 'gateway-api-support', source: 'gateway-api-support/index.md' },
  {
    slug: 'launch-blog-post',
    source: 'launch/kthena_llm_inference.mdx',
    omittedHeadingIds: ['2-out-of-the-box-model-onboarding-modelbooster'],
  },
  { slug: 'modelserving-blog-post', source: 'modelserving/index.md' },
  { slug: 'release-v0.3.0', source: 'release-v0.3.0/index.md' },
  { slug: 'release-v0.4.0', source: 'release-v0.4.0/index.md' },
  {
    slug: 'release-v1.0.0',
    source: 'release-v1.0.0/index.md',
    headingAliases: {
      'modelserving-controller': 'modelserving-and-modelbooster-controllers',
      'new-router-configuration': 'new-modelbooster-and-router-configuration',
    },
  },
  { slug: 'router-blog-post', source: 'router/index.md' },
];

test('the Chinese blog index lists every translated post', () => {
  const index = page('/zh-Hans/blog');
  assert.doesNotMatch(index, /此页面尚未翻译/);
  for (const { slug } of translatedPosts) {
    assert.ok(hasLink(index, `/zh-Hans/blog/${slug}`));
  }
});

for (const {
  slug,
  source,
  omittedHeadingIds = [],
  headingAliases = {},
} of translatedPosts) {
  const route = `/blog/${slug}`;
  test(`translated blog post preserves navigation and section links: ${route}`, () => {
    const en = page(route);
    const zh = page(`/zh-Hans${route}`);
    assert.ok(hasLink(en, `/zh-Hans${route}`, 'zh-Hans'));
    assert.ok(hasLink(zh, route, 'en'));
    assert.doesNotMatch(zh, /此页面尚未翻译/);

    const headings = (html) =>
      tags(html, 'h[2-6]').map((tag) => attribute(tag, 'id'));
    assert.deepEqual(
      headings(zh).map((id) => headingAliases[id] ?? id),
      headings(en).filter((id) => !omittedHeadingIds.includes(id)),
      'section IDs must survive a language switch',
    );

    const links = tags(zh, 'link');
    assert.ok(
      links.some(
        (tag) =>
          attribute(tag, 'rel') === 'canonical' &&
          attribute(tag, 'href') ===
            `https://kthena.volcano.sh/zh-Hans${route}`,
      ),
    );
    assert.ok(
      links.some(
        (tag) =>
          attribute(tag, 'hreflang') === 'en' &&
          attribute(tag, 'href') === `https://kthena.volcano.sh${route}`,
      ),
    );
    assert.ok(
      hasLink(
        zh,
        `https://github.com/volcano-sh/kthena/tree/main/docs/kthena/i18n/zh-Hans/docusaurus-plugin-content-blog/${source}`,
      ),
      'edit link must point to the translated source',
    );

    for (const image of tags(zh, 'img')) {
      const src = attribute(image, 'src');
      if (src?.startsWith('/')) {
        assert.ok(
          existsSync(new URL(`../build${src}`, import.meta.url)),
          `missing image ${src}`,
        );
      }
    }
  });
}
