// SPDX-License-Identifier: GPL-3.0-only
// Run with Node and Playwright WebKit installed; this uses the actual bundled HTMX and app script.
const fs=require('fs'),http=require('http');
const {webkit}=require('playwright');
const root=require('path').resolve(__dirname,'..');
const main=(path)=>`<a class="page-back" data-page-back href="/parent" hx-get="/parent" hx-target="#content" hx-push-url="true">← Back</a><header><h1>${path}</h1></header><a id="child" href="/child" hx-get="/child" hx-target="#content" hx-push-url="true">Album</a><a id="tab" href="/tab" hx-get="/tab" hx-target="#content" hx-push-url="true">Podcasts</a><a id="next" href="/next" hx-get="/next" hx-target="#content" hx-push-url="true">Next</a><form id="edit" hx-post="/edit" hx-target="#content"><button>Save</button></form><div style="height:3500px"></div>`;
const full=path=>`<!doctype html><meta name="htmx-config" content='{"allowEval":false,"includeIndicatorStyles":false,"historyRestoreAsHxRequest":false,"historyCacheSize":0}'><script src="/htmx.js" defer></script><script src="/app.js" defer></script><div class="app" hx-history-elt><main id="content" tabindex="-1">${main(path)}</main></div>`;
const server=http.createServer((req,res)=>{if(req.url==='/htmx.js'||req.url==='/app.js'){res.setHeader('Content-Type','application/javascript');res.end(fs.readFileSync(root+'/internal/web/static/'+(req.url==='/htmx.js'?'htmx.min.js':'app.js')));return;}res.setHeader('Content-Type','text/html');res.end(req.headers['hx-request']?main(req.url):full(req.url));});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await webkit.launch({headless:true});const page=await browser.newPage({viewport:{width:375,height:812}});const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(`http://127.0.0.1:${server.address().port}/parent`);await page.waitForTimeout(100);
const scroll=async y=>{await page.evaluate(y=>window.scrollTo(0,y),y);await page.waitForTimeout(50)};
const check=async(label,want)=>{await page.waitForTimeout(150);const actual=await page.evaluate(()=>scrollY);if(Math.abs(actual-want)>2)throw Error(`${label}: wanted ${want}, got ${actual}`);console.log(`${label}: ${actual}`)};
await scroll(600);await page.evaluate(()=>document.getElementById('child').click());await check('album navigation starts at top',0);
await scroll(450);await page.goBack();await check('browser Back restores parent',600);
await page.goForward();await check('browser Forward restores album',450);
await page.evaluate(()=>document.querySelector('[data-page-back]').click());await check('Back link restores parent',600);
await page.evaluate(()=>document.getElementById('tab').click());await check('section navigation starts at top',0);
await scroll(700);await page.evaluate(()=>document.getElementById('next').click());await check('pagination starts at top',0);
await scroll(500);await page.evaluate(()=>htmx.ajax('GET','/poll',{target:'#content',swap:'innerHTML'}));await check('background refresh preserves position',500);
await page.evaluate(()=>document.getElementById('edit').requestSubmit());await check('in-page POST preserves position',500);
console.log('browser errors:',JSON.stringify(errors));if(errors.length)throw Error('unexpected browser errors');
await browser.close();server.close();})().catch(e=>{console.error(e);server.close();process.exitCode=1});
