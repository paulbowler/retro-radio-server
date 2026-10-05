// SPDX-License-Identifier: GPL-3.0-only
(() => {
 const dialog = document.getElementById("confirmation");
 if (!dialog || !dialog.showModal) return;
 const title = document.getElementById("confirmation-title");
 const message = document.getElementById("confirmation-message");
 const accept = document.getElementById("confirmation-accept");
 const result = document.getElementById("result");
 function showResult(detail) {
  const feedDialog = document.getElementById("add-podcast-feed");
  if (feedDialog?.open) feedDialog.close();
  if (!result || result.open) return;
  result.classList.toggle("error", !detail.success);
  document.getElementById("result-icon").textContent = detail.success ? "✓" : "!";
  document.getElementById("result-title").textContent = detail.title;
  document.getElementById("result-message").textContent = detail.message;
  result.showModal();
 }
 document.addEventListener("station-result", event => showResult(event.detail));
 for (const name of ["htmx:sendError", "htmx:timeout", "htmx:responseError"]) {
  document.addEventListener(name, event => {
   if (!event.detail.elt?.matches("[data-result-feedback]")) return;
   showResult({success: false, title: event.detail.elt.dataset.errorTitle || "Couldn’t add station", message: "Couldn’t finish saving. Please try again."});
  });
 }
 let pending = null;
 document.addEventListener("htmx:confirm", event => {
  if (!event.detail.question) return;
  event.preventDefault();
  if (dialog.open) return;
  pending = event.detail;
  const card = pending.elt.closest(".station-card");
  if (card && window.htmx) window.htmx.trigger(card, "htmx:abort");
  title.textContent = pending.elt.dataset.confirmTitle || "Confirm action";
  message.textContent = pending.question;
  accept.textContent = pending.elt.dataset.confirmLabel || "Confirm";
  dialog.returnValue = "cancel";
  dialog.showModal();
 });
 dialog.addEventListener("close", () => {
  const request = pending;
  pending = null;
  if (dialog.returnValue === "confirm" && request && request.elt.isConnected) request.issueRequest(true);
 });
 document.addEventListener("htmx:beforeHistorySave", () => {
  if (dialog.open) dialog.close("cancel");
 });
})();

// Suggest a country from regional settings without sending location to a third party.
(() => {
 const zones = {"Europe/Andorra":"AD","Asia/Dubai":"AE","Asia/Kabul":"AF","America/Antigua":"AG","America/Anguilla":"AI","Europe/Tirane":"AL","Asia/Yerevan":"AM","Africa/Luanda":"AO","Antarctica/McMurdo":"AQ","Antarctica/Casey":"AQ","Antarctica/Davis":"AQ","Antarctica/DumontDUrville":"AQ","Antarctica/Mawson":"AQ","Antarctica/Palmer":"AQ","Antarctica/Rothera":"AQ","Antarctica/Syowa":"AQ","Antarctica/Troll":"AQ","Antarctica/Vostok":"AQ","America/Argentina/Buenos_Aires":"AR","America/Argentina/Cordoba":"AR","America/Argentina/Salta":"AR","America/Argentina/Jujuy":"AR","America/Argentina/Tucuman":"AR","America/Argentina/Catamarca":"AR","America/Argentina/La_Rioja":"AR","America/Argentina/San_Juan":"AR","America/Argentina/Mendoza":"AR","America/Argentina/San_Luis":"AR","America/Argentina/Rio_Gallegos":"AR","America/Argentina/Ushuaia":"AR","Pacific/Pago_Pago":"AS","Europe/Vienna":"AT","Australia/Lord_Howe":"AU","Antarctica/Macquarie":"AU","Australia/Hobart":"AU","Australia/Melbourne":"AU","Australia/Sydney":"AU","Australia/Broken_Hill":"AU","Australia/Brisbane":"AU","Australia/Lindeman":"AU","Australia/Adelaide":"AU","Australia/Darwin":"AU","Australia/Perth":"AU","Australia/Eucla":"AU","America/Aruba":"AW","Europe/Mariehamn":"AX","Asia/Baku":"AZ","Europe/Sarajevo":"BA","America/Barbados":"BB","Asia/Dhaka":"BD","Europe/Brussels":"BE","Africa/Ouagadougou":"BF","Europe/Sofia":"BG","Asia/Bahrain":"BH","Africa/Bujumbura":"BI","Africa/Porto-Novo":"BJ","America/St_Barthelemy":"BL","Atlantic/Bermuda":"BM","Asia/Brunei":"BN","America/La_Paz":"BO","America/Kralendijk":"BQ","America/Noronha":"BR","America/Belem":"BR","America/Fortaleza":"BR","America/Recife":"BR","America/Araguaina":"BR","America/Maceio":"BR","America/Bahia":"BR","America/Sao_Paulo":"BR","America/Campo_Grande":"BR","America/Cuiaba":"BR","America/Santarem":"BR","America/Porto_Velho":"BR","America/Boa_Vista":"BR","America/Manaus":"BR","America/Eirunepe":"BR","America/Rio_Branco":"BR","America/Nassau":"BS","Asia/Thimphu":"BT","Africa/Gaborone":"BW","Europe/Minsk":"BY","America/Belize":"BZ","America/St_Johns":"CA","America/Halifax":"CA","America/Glace_Bay":"CA","America/Moncton":"CA","America/Goose_Bay":"CA","America/Blanc-Sablon":"CA","America/Toronto":"CA","America/Iqaluit":"CA","America/Atikokan":"CA","America/Winnipeg":"CA","America/Resolute":"CA","America/Rankin_Inlet":"CA","America/Regina":"CA","America/Swift_Current":"CA","America/Edmonton":"CA","America/Cambridge_Bay":"CA","America/Inuvik":"CA","America/Vancouver":"CA","America/Creston":"CA","America/Dawson_Creek":"CA","America/Fort_Nelson":"CA","America/Whitehorse":"CA","America/Dawson":"CA","Indian/Cocos":"CC","Africa/Kinshasa":"CD","Africa/Lubumbashi":"CD","Africa/Bangui":"CF","Africa/Brazzaville":"CG","Europe/Zurich":"CH","Africa/Abidjan":"CI","Pacific/Rarotonga":"CK","America/Santiago":"CL","America/Coyhaique":"CL","America/Punta_Arenas":"CL","Pacific/Easter":"CL","Africa/Douala":"CM","Asia/Shanghai":"CN","Asia/Urumqi":"CN","America/Bogota":"CO","America/Costa_Rica":"CR","America/Havana":"CU","Atlantic/Cape_Verde":"CV","America/Curacao":"CW","Indian/Christmas":"CX","Asia/Nicosia":"CY","Asia/Famagusta":"CY","Europe/Prague":"CZ","Europe/Berlin":"DE","Europe/Busingen":"DE","Africa/Djibouti":"DJ","Europe/Copenhagen":"DK","America/Dominica":"DM","America/Santo_Domingo":"DO","Africa/Algiers":"DZ","America/Guayaquil":"EC","Pacific/Galapagos":"EC","Europe/Tallinn":"EE","Africa/Cairo":"EG","Africa/El_Aaiun":"EH","Africa/Asmara":"ER","Europe/Madrid":"ES","Africa/Ceuta":"ES","Atlantic/Canary":"ES","Africa/Addis_Ababa":"ET","Europe/Helsinki":"FI","Pacific/Fiji":"FJ","Atlantic/Stanley":"FK","Pacific/Chuuk":"FM","Pacific/Pohnpei":"FM","Pacific/Kosrae":"FM","Atlantic/Faroe":"FO","Europe/Paris":"FR","Africa/Libreville":"GA","Europe/London":"GB","America/Grenada":"GD","Asia/Tbilisi":"GE","America/Cayenne":"GF","Europe/Guernsey":"GG","Africa/Accra":"GH","Europe/Gibraltar":"GI","America/Nuuk":"GL","America/Danmarkshavn":"GL","America/Scoresbysund":"GL","America/Thule":"GL","Africa/Banjul":"GM","Africa/Conakry":"GN","America/Guadeloupe":"GP","Africa/Malabo":"GQ","Europe/Athens":"GR","Atlantic/South_Georgia":"GS","America/Guatemala":"GT","Pacific/Guam":"GU","Africa/Bissau":"GW","America/Guyana":"GY","Asia/Hong_Kong":"HK","America/Tegucigalpa":"HN","Europe/Zagreb":"HR","America/Port-au-Prince":"HT","Europe/Budapest":"HU","Asia/Jakarta":"ID","Asia/Pontianak":"ID","Asia/Makassar":"ID","Asia/Jayapura":"ID","Europe/Dublin":"IE","Asia/Jerusalem":"IL","Europe/Isle_of_Man":"IM","Asia/Kolkata":"IN","Indian/Chagos":"IO","Asia/Baghdad":"IQ","Asia/Tehran":"IR","Atlantic/Reykjavik":"IS","Europe/Rome":"IT","Europe/Jersey":"JE","America/Jamaica":"JM","Asia/Amman":"JO","Asia/Tokyo":"JP","Africa/Nairobi":"KE","Asia/Bishkek":"KG","Asia/Phnom_Penh":"KH","Pacific/Tarawa":"KI","Pacific/Kanton":"KI","Pacific/Kiritimati":"KI","Indian/Comoro":"KM","America/St_Kitts":"KN","Asia/Pyongyang":"KP","Asia/Seoul":"KR","Asia/Kuwait":"KW","America/Cayman":"KY","Asia/Almaty":"KZ","Asia/Qyzylorda":"KZ","Asia/Qostanay":"KZ","Asia/Aqtobe":"KZ","Asia/Aqtau":"KZ","Asia/Atyrau":"KZ","Asia/Oral":"KZ","Asia/Vientiane":"LA","Asia/Beirut":"LB","America/St_Lucia":"LC","Europe/Vaduz":"LI","Asia/Colombo":"LK","Africa/Monrovia":"LR","Africa/Maseru":"LS","Europe/Vilnius":"LT","Europe/Luxembourg":"LU","Europe/Riga":"LV","Africa/Tripoli":"LY","Africa/Casablanca":"MA","Europe/Monaco":"MC","Europe/Chisinau":"MD","Europe/Podgorica":"ME","America/Marigot":"MF","Indian/Antananarivo":"MG","Pacific/Majuro":"MH","Pacific/Kwajalein":"MH","Europe/Skopje":"MK","Africa/Bamako":"ML","Asia/Yangon":"MM","Asia/Ulaanbaatar":"MN","Asia/Hovd":"MN","Asia/Macau":"MO","Pacific/Saipan":"MP","America/Martinique":"MQ","Africa/Nouakchott":"MR","America/Montserrat":"MS","Europe/Malta":"MT","Indian/Mauritius":"MU","Indian/Maldives":"MV","Africa/Blantyre":"MW","America/Mexico_City":"MX","America/Cancun":"MX","America/Merida":"MX","America/Monterrey":"MX","America/Matamoros":"MX","America/Chihuahua":"MX","America/Ciudad_Juarez":"MX","America/Ojinaga":"MX","America/Mazatlan":"MX","America/Bahia_Banderas":"MX","America/Hermosillo":"MX","America/Tijuana":"MX","Asia/Kuala_Lumpur":"MY","Asia/Kuching":"MY","Africa/Maputo":"MZ","Africa/Windhoek":"NA","Pacific/Noumea":"NC","Africa/Niamey":"NE","Pacific/Norfolk":"NF","Africa/Lagos":"NG","America/Managua":"NI","Europe/Amsterdam":"NL","Europe/Oslo":"NO","Asia/Kathmandu":"NP","Pacific/Nauru":"NR","Pacific/Niue":"NU","Pacific/Auckland":"NZ","Pacific/Chatham":"NZ","Asia/Muscat":"OM","America/Panama":"PA","America/Lima":"PE","Pacific/Tahiti":"PF","Pacific/Marquesas":"PF","Pacific/Gambier":"PF","Pacific/Port_Moresby":"PG","Pacific/Bougainville":"PG","Asia/Manila":"PH","Asia/Karachi":"PK","Europe/Warsaw":"PL","America/Miquelon":"PM","Pacific/Pitcairn":"PN","America/Puerto_Rico":"PR","Asia/Gaza":"PS","Asia/Hebron":"PS","Europe/Lisbon":"PT","Atlantic/Madeira":"PT","Atlantic/Azores":"PT","Pacific/Palau":"PW","America/Asuncion":"PY","Asia/Qatar":"QA","Indian/Reunion":"RE","Europe/Bucharest":"RO","Europe/Belgrade":"RS","Europe/Kaliningrad":"RU","Europe/Moscow":"RU","Europe/Simferopol":"UA","Europe/Kirov":"RU","Europe/Volgograd":"RU","Europe/Astrakhan":"RU","Europe/Saratov":"RU","Europe/Ulyanovsk":"RU","Europe/Samara":"RU","Asia/Yekaterinburg":"RU","Asia/Omsk":"RU","Asia/Novosibirsk":"RU","Asia/Barnaul":"RU","Asia/Tomsk":"RU","Asia/Novokuznetsk":"RU","Asia/Krasnoyarsk":"RU","Asia/Irkutsk":"RU","Asia/Chita":"RU","Asia/Yakutsk":"RU","Asia/Khandyga":"RU","Asia/Vladivostok":"RU","Asia/Ust-Nera":"RU","Asia/Magadan":"RU","Asia/Sakhalin":"RU","Asia/Srednekolymsk":"RU","Asia/Kamchatka":"RU","Asia/Anadyr":"RU","Africa/Kigali":"RW","Asia/Riyadh":"SA","Pacific/Guadalcanal":"SB","Indian/Mahe":"SC","Africa/Khartoum":"SD","Europe/Stockholm":"SE","Asia/Singapore":"SG","Atlantic/St_Helena":"SH","Europe/Ljubljana":"SI","Arctic/Longyearbyen":"SJ","Europe/Bratislava":"SK","Africa/Freetown":"SL","Europe/San_Marino":"SM","Africa/Dakar":"SN","Africa/Mogadishu":"SO","America/Paramaribo":"SR","Africa/Juba":"SS","Africa/Sao_Tome":"ST","America/El_Salvador":"SV","America/Lower_Princes":"SX","Asia/Damascus":"SY","Africa/Mbabane":"SZ","America/Grand_Turk":"TC","Africa/Ndjamena":"TD","Indian/Kerguelen":"TF","Africa/Lome":"TG","Asia/Bangkok":"TH","Asia/Dushanbe":"TJ","Pacific/Fakaofo":"TK","Asia/Dili":"TL","Asia/Ashgabat":"TM","Africa/Tunis":"TN","Pacific/Tongatapu":"TO","Europe/Istanbul":"TR","America/Port_of_Spain":"TT","Pacific/Funafuti":"TV","Asia/Taipei":"TW","Africa/Dar_es_Salaam":"TZ","Europe/Kyiv":"UA","Africa/Kampala":"UG","Pacific/Midway":"UM","Pacific/Wake":"UM","America/New_York":"US","America/Detroit":"US","America/Kentucky/Louisville":"US","America/Kentucky/Monticello":"US","America/Indiana/Indianapolis":"US","America/Indiana/Vincennes":"US","America/Indiana/Winamac":"US","America/Indiana/Marengo":"US","America/Indiana/Petersburg":"US","America/Indiana/Vevay":"US","America/Chicago":"US","America/Indiana/Tell_City":"US","America/Indiana/Knox":"US","America/Menominee":"US","America/North_Dakota/Center":"US","America/North_Dakota/New_Salem":"US","America/North_Dakota/Beulah":"US","America/Denver":"US","America/Boise":"US","America/Phoenix":"US","America/Los_Angeles":"US","America/Anchorage":"US","America/Juneau":"US","America/Sitka":"US","America/Metlakatla":"US","America/Yakutat":"US","America/Nome":"US","America/Adak":"US","Pacific/Honolulu":"US","America/Montevideo":"UY","Asia/Samarkand":"UZ","Asia/Tashkent":"UZ","Europe/Vatican":"VA","America/St_Vincent":"VC","America/Caracas":"VE","America/Tortola":"VG","America/St_Thomas":"VI","Asia/Ho_Chi_Minh":"VN","Pacific/Efate":"VU","Pacific/Wallis":"WF","Pacific/Apia":"WS","Asia/Aden":"YE","Indian/Mayotte":"YT","Africa/Johannesburg":"ZA","Africa/Lusaka":"ZM","Africa/Harare":"ZW"};
 function suggestCountry() {
  const picker = document.querySelector("#listener-country[data-country-auto]");
  if (!picker) return;
  let code = zones[Intl.DateTimeFormat().resolvedOptions().timeZone];
  if (!code) {
   for (const language of navigator.languages || [navigator.language]) {
    try { code = new Intl.Locale(language).region; } catch (_) { continue; }
    if (code) break;
   }
  }
  if (code && Array.from(picker.options).some(option => option.value === code)) picker.value = code;
  picker.removeAttribute("data-country-auto");
 }
 suggestCountry();
 document.addEventListener("htmx:load", suggestCountry);
})();

// Show listening times in the visitor’s local timezone.
(() => {
 const format = new Intl.DateTimeFormat(undefined, {day:"numeric",month:"short",hour:"2-digit",minute:"2-digit"});
 function localTimes() {
  for (const element of document.querySelectorAll("time[datetime]")) {
   const date = new Date(element.dateTime);
   if (!Number.isNaN(date.getTime())) element.textContent = format.format(date);
  }
 }
 localTimes();
 document.addEventListener("htmx:load", localTimes);
})();

// The dialog stays outside page fragments, so radio navigation can update normally.
(() => {
 const dialog = document.getElementById("rename-radio");
 const form = document.getElementById("rename-radio-form");
 if (!dialog || !form || !dialog.showModal) return;
 const name = document.getElementById("rename-radio-name");
 const error = document.getElementById("rename-radio-error");
 const busy = () => form.classList.contains("htmx-request");
 document.addEventListener("click", event => {
  const trigger = event.target.closest("[data-rename-radio]");
  if (trigger) {
   form.elements.device.value = trigger.dataset.renameRadio;
   name.value = trigger.dataset.radioName;
   name.setCustomValidity("");
   error.hidden = true;
   dialog.showModal();
   name.focus();
   name.select();
  }
  if (event.target.closest("[data-cancel-rename]") && !busy()) dialog.close();
 });
 dialog.addEventListener("cancel", event => { if (busy()) event.preventDefault(); });
 name.addEventListener("input", () => { name.setCustomValidity(""); error.hidden = true; });
 form.addEventListener("submit", event => {
  name.value = name.value.trim();
  if (!name.value) {
   event.preventDefault();
   event.stopImmediatePropagation();
   name.setCustomValidity("Enter a name for your radio.");
   name.reportValidity();
  }
 }, true);
 document.addEventListener("htmx:beforeRequest", event => {
  if (event.detail.elt === form) error.hidden = true;
  else if (event.detail.target?.id === "content" && dialog.open && !busy()) dialog.close();
 });
 document.addEventListener("htmx:afterRequest", event => {
  if (event.detail.elt !== form) return;
  if (event.detail.successful) dialog.close();
  else {
   error.textContent = event.detail.xhr?.status === 400
    ? "Enter a shorter name for your radio."
    : "Couldn’t save the name. Please try again.";
   error.hidden = false;
  }
 });
})();

// Keep browser listening to one station or episode at a time.
document.addEventListener("play", event => {
 if (!(event.target instanceof HTMLAudioElement)) return;
 for (const player of document.querySelectorAll("audio")) if (player !== event.target) player.pause();
}, true);

// Keep the feed form in the persistent shell, like the radio rename dialog.
(() => {
 const dialog = document.getElementById("add-podcast-feed");
 const form = document.getElementById("add-podcast-feed-form");
 const link = document.getElementById("podcast-feed-link");
 if (!dialog || !form || !dialog.showModal) return;
 const busy = () => form.classList.contains("htmx-request");
 document.addEventListener("click", event => {
  if (event.target.closest("[data-add-podcast-feed]")) {
   form.elements.q.value = document.getElementById("podcast-query")?.value || "";
   dialog.showModal();
   link.focus();
  }
  if (event.target.closest("[data-cancel-podcast-feed]") && !busy()) dialog.close();
 });
 dialog.addEventListener("cancel", event => { if (busy()) event.preventDefault(); });
 document.addEventListener("htmx:beforeRequest", event => {
  if (event.detail.elt !== form && event.detail.target?.id === "content" && dialog.open && !busy()) dialog.close();
 });
 document.addEventListener("htmx:afterRequest", event => {
  if (event.detail.elt === form && event.detail.successful && event.detail.xhr?.status === 200) {
   dialog.close();
   form.reset();
  }
 });
 document.addEventListener("htmx:beforeHistorySave", () => {
  if (dialog.open && !busy()) dialog.close();
 });
})();

// Pausing live radio releases the connection instead of buffering it indefinitely.
document.addEventListener("pause", event => {
 if (event.target instanceof HTMLAudioElement && event.target.matches(".station-player") && event.target.readyState > 0) event.target.load();
}, true);

// Keep the station placeholder visible if directory artwork is unavailable.
document.addEventListener("error", event => { if (event.target.matches?.("img[data-station-artwork]")) event.target.hidden=true; },true);

(() => {
 const dialog=document.getElementById("preferences"), content=document.getElementById("preferences-content");
 document.addEventListener("click",event=>{
  if(event.target.closest("[data-open-preferences]")){dialog.showModal();content.textContent="";htmx.ajax("GET","/preferences",{target:content,swap:"innerHTML"});}
  if(event.target.closest("[data-close-preferences]"))dialog.close();
 });
 document.addEventListener("input",event=>{if(event.target.id==="playback-buffer")document.getElementById("buffer-value").textContent=event.target.value+"s";});
 document.addEventListener("htmx:afterRequest",event=>{
  if(event.detail.elt.id!=="preferences-form")return;
  if(event.detail.successful){dialog.close();return;}
  const error=document.getElementById("preferences-error");if(error){error.textContent="Couldn’t save settings. Please try again.";error.hidden=false;}
 });
})();

// Page navigation starts at the top; updates within a page preserve its position.
// HTMX's disabled history cache keeps playback tokens fresh, so restore scroll
// independently when a history miss fetches the page again.
(() => {
 const positions = new Map();
 const requests = new WeakMap();
 let current = location.pathname + location.search;
 const key = value => {
  const url = new URL(value, location.href);
  return url.pathname + url.search;
 };
 const remember = () => {
  positions.delete(current);
  positions.set(current, window.scrollY);
  if (positions.size > 100) positions.delete(positions.keys().next().value);
 };
 const move = (position, focus) => {
  if (focus) document.getElementById("content")?.focus({preventScroll:true});
  window.scrollTo({top:position, left:0, behavior:"instant"});
 };
 document.addEventListener("htmx:beforeRequest", event => {
  const {target, xhr, requestConfig:config, elt} = event.detail;
  if (target?.id !== "content" || config?.verb?.toLowerCase() !== "get") return;
  const trigger = config.triggeringEvent;
  // Timers, initial loads and programmatic refreshes are not navigation.
  if (!trigger || !["click", "submit", "change", "search"].includes(trigger.type)) return;
  remember();
  const back = elt.closest?.("[data-page-back]");
  requests.set(xhr, {position:back ? (positions.get(key(back.href)) || 0) : 0});
 });
 document.addEventListener("htmx:afterSettle", event => {
  if (event.detail.target?.id !== "content") return;
  const navigation = requests.get(event.detail.xhr);
  if (!navigation) return;
  requests.delete(event.detail.xhr);
  current = location.pathname + location.search;
  move(navigation.position, true);
 });
 document.addEventListener("htmx:pushedIntoHistory", () => {
  current = location.pathname + location.search;
 });
 document.addEventListener("htmx:replacedInHistory", () => {
  current = location.pathname + location.search;
 });
 window.addEventListener("popstate", () => {
  remember();
  current = location.pathname + location.search;
 });
 document.addEventListener("htmx:historyRestore", event => {
  current = key(event.detail.path || location.href);
  const position = positions.get(current) || 0;
  // History cache misses settle asynchronously, unlike cache hits.
  requestAnimationFrame(() => requestAnimationFrame(() => move(position, false)));
 });
})();
