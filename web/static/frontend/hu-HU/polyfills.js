function s(t,e){for(let i=1,n=1;i<t.length;i++,n++)if(e[n]==="\\")n++;else if(t[i]===":")return i;throw new Error(`Unterminated $localize metadata block in "${e}".`)}var o=function(t,...e){if(o.translate){let n=o.translate(t,e);t=n[0],e=n[1]}let i=r(t[0],t.raw[0]);for(let n=1;n<t.length;n++)i+=e[n-1]+r(t[n],t.raw[n]);return i},l=":";function r(t,e){return e.charAt(0)===l?t.substring(s(t,e)+1):t}globalThis.$localize=o;(globalThis.$localize??={}).locale="hu-HU";
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
const n = val;

if (n === 1)
    return 1;
return 5;
}
    global.ng.common.locales['hu'] = ["hu",[["de.","du."]],u,[["V","H","K","Sz","Cs","P","Sz"],["V","H","K","Sze","Cs","P","Szo"],["vasárnap","hétfő","kedd","szerda","csütörtök","péntek","szombat"],["V","H","K","Sze","Cs","P","Szo"]],u,[["J","F","M","Á","M","J","J","A","Sz","O","N","D"],["jan.","febr.","márc.","ápr.","máj.","jún.","júl.","aug.","szept.","okt.","nov.","dec."],["január","február","március","április","május","június","július","augusztus","szeptember","október","november","december"]],u,[["ie.","isz."],["i. e.","i. sz."],["Krisztus előtt","időszámításunk szerint"]],1,[6,0],["y. MM. dd.","y. MMM d.","y. MMMM d.","y. MMMM d., EEEE"],["H:mm","H:mm:ss","H:mm:ss z","H:mm:ss zzzz"],["{1} {0}",u,u,u],[","," ",";","%","+","-","E","×","‰","∞","NaN",":"],["#,##0.###","#,##0%","#,##0.00 ¤","#E0"],"HUF","Ft","magyar forint",{"AUD":[u,"$"],"BRL":[u,"R$"],"BYN":[u,"р."],"CAD":[u,"$"],"CNY":[u,"¥"],"EUR":[u,"€"],"GBP":[u,"£"],"HKD":[u,"$"],"HUF":["Ft"],"ILS":[u,"₪"],"INR":[u,"₹"],"KRW":[u,"₩"],"MXN":[u,"$"],"NZD":[u,"$"],"PHP":[u,"₱"],"TWD":[u,"NT$"],"USD":[u,"$"],"VND":[u,"₫"],"XCD":[u,"$"]},"ltr", plural, [[["éjfél","dél","reggel","de.","du.","este","éjjel","hajnal"],u,["éjfél","dél","reggel","délelőtt","délután","este","éjjel","hajnal"]],u,["00:00","12:00",["06:00","09:00"],["09:00","12:00"],["12:00","18:00"],["18:00","21:00"],["21:00","04:00"],["04:00","06:00"]]]];
  })(globalThis);
    
;;
/**i18n:fa3d7e19b336ec75*/
