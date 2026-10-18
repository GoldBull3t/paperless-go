function s(t,e){for(let i=1,n=1;i<t.length;i++,n++)if(e[n]==="\\")n++;else if(t[i]===":")return i;throw new Error(`Unterminated $localize metadata block in "${e}".`)}var o=function(t,...e){if(o.translate){let n=o.translate(t,e);t=n[0],e=n[1]}let i=r(t[0],t.raw[0]);for(let n=1;n<t.length;n++)i+=e[n-1]+r(t[n],t.raw[n]);return i},l=":";function r(t,e){return e.charAt(0)===l?t.substring(s(t,e)+1):t}globalThis.$localize=o;(globalThis.$localize??={}).locale="lb-LU";
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
    global.ng.common.locales['lb'] = ["lb",[["mo.","nomë."],["moies","nomëttes"]],[["moies","nomëttes"]],[["S","M","D","M","D","F","S"],["Son.","Méi.","Dën.","Mët.","Don.","Fre.","Sam."],["Sonndeg","Méindeg","Dënschdeg","Mëttwoch","Donneschdeg","Freideg","Samschdeg"],["So.","Mé.","Dë.","Më.","Do.","Fr.","Sa."]],[["S","M","D","M","D","F","S"],["Son","Méi","Dën","Mët","Don","Fre","Sam"],["Sonndeg","Méindeg","Dënschdeg","Mëttwoch","Donneschdeg","Freideg","Samschdeg"],["So.","Mé.","Dë.","Më.","Do.","Fr.","Sa."]],[["J","F","M","A","M","J","J","A","S","O","N","D"],["Jan.","Feb.","Mäe.","Abr.","Mee","Juni","Juli","Aug.","Sep.","Okt.","Nov.","Dez."],["Januar","Februar","Mäerz","Abrëll","Mee","Juni","Juli","August","September","Oktober","November","Dezember"]],[["J","F","M","A","M","J","J","A","S","O","N","D"],["Jan","Feb","Mäe","Abr","Mee","Jun","Jul","Aug","Sep","Okt","Nov","Dez"],["Januar","Februar","Mäerz","Abrëll","Mee","Juni","Juli","August","September","Oktober","November","Dezember"]],[["v. Chr.","n. Chr."]],1,[6,0],["dd.MM.yy","d. MMM y","d. MMMM y","EEEE, d. MMMM y"],["HH:mm","HH:mm:ss","HH:mm:ss z","HH:mm:ss zzzz"],["{1} {0}",u,u,u],[",",".",";","%","+","-","E","×","‰","∞","NaN",":"],["#,##0.###","#,##0 %","#,##0.00 ¤","#E0"],"EUR","€","Euro",{"ATS":["öS"],"AUD":["AU$","$"],"PHP":[u,"₱"],"THB":["฿"],"TWD":["NT$"]},"ltr", plural, []];
  })(globalThis);
    
;;
/**i18n:fa3d7e19b336ec75*/
