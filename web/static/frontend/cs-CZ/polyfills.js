function s(t,e){for(let i=1,n=1;i<t.length;i++,n++)if(e[n]==="\\")n++;else if(t[i]===":")return i;throw new Error(`Unterminated $localize metadata block in "${e}".`)}var o=function(t,...e){if(o.translate){let n=o.translate(t,e);t=n[0],e=n[1]}let i=r(t[0],t.raw[0]);for(let n=1;n<t.length;n++)i+=e[n-1]+r(t[n],t.raw[n]);return i},l=":";function r(t,e){return e.charAt(0)===l?t.substring(s(t,e)+1):t}globalThis.$localize=o;(globalThis.$localize??={}).locale="cs-CZ";
/**
 * @license
 * Copyright Google LLC All Rights Reserved.
 *
 * Use of this source code is governed by an MIT-style license that can be
 * found in the LICENSE file at https://angular.dev/license
 */

// THIS CODE IS GENERATED - DO NOT MODIFY.
  (function(global) {
    global.ng ??= {};
    global.ng.common ??= {};
    global.ng.common.locales ??= {};
    const u = undefined;
    function plural(val) {
const n = val, i = Math.floor(Math.abs(val)), v = val.toString().replace(/^[^.]*\.?/, '').length;

if (i === 1 && v === 0)
    return 1;
if (i === Math.floor(i) && (i >= 2 && i <= 4) && v === 0)
    return 3;
if (!(v === 0))
    return 4;
return 5;
}
    global.ng.common.locales['cs'] = ["cs",[["dop.","odp."]],u,[["N","P","Ú","S","Č","P","S"],["ne","po","út","st","čt","pá","so"],["neděle","pondělí","úterý","středa","čtvrtek","pátek","sobota"],["ne","po","út","st","čt","pá","so"]],u,[["1","2","3","4","5","6","7","8","9","10","11","12"],["led","úno","bře","dub","kvě","čvn","čvc","srp","zář","říj","lis","pro"],["ledna","února","března","dubna","května","června","července","srpna","září","října","listopadu","prosince"]],[["1","2","3","4","5","6","7","8","9","10","11","12"],["led","úno","bře","dub","kvě","čvn","čvc","srp","zář","říj","lis","pro"],["leden","únor","březen","duben","květen","červen","červenec","srpen","září","říjen","listopad","prosinec"]],[["př.n.l.","n.l."],["př. n. l.","n. l."],["před naším letopočtem","našeho letopočtu"]],1,[6,0],["dd.MM.yy","d. M. y","d. MMMM y","EEEE d. MMMM y"],["H:mm","H:mm:ss","H:mm:ss z","H:mm:ss, zzzz"],["{1} {0}",u,u,u],[","," ",";","%","+","-","E","×","‰","∞","NaN",":"],["#,##0.###","#,##0 %","#,##0.00 ¤","#E0"],"CZK","Kč","česká koruna",{"AUD":["AU$","$"],"BYN":[u,"р."],"CSK":["Kčs"],"CZK":["Kč"],"ILS":[u,"₪"],"INR":[u,"₹"],"JPY":["JP¥","¥"],"PHP":[u,"₱"],"RON":[u,"L"],"RUR":[u,"р."],"TWD":["NT$"],"USD":["US$","$"],"VND":[u,"₫"],"XEU":["ECU"],"XXX":[]},"ltr", plural, [[["půl.","pol.","r.","d.","o.","v.","n."],["půln.","pol.","r.","dop.","odp.","več.","v n."],["půlnoc","poledne","ráno","dopoledne","odpoledne","večer","v noci"]],[["půl.","pol.","ráno","dop.","odp.","več.","noc"],["půlnoc","poledne","ráno","dopoledne","odpoledne","večer","noc"],u],["00:00","12:00",["04:00","09:00"],["09:00","12:00"],["12:00","18:00"],["18:00","22:00"],["22:00","04:00"]]]];
  })(globalThis);
    
;;
/**i18n:fa3d7e19b336ec75*/
